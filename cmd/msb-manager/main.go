// Command msb-manager runs the control-plane HTTP server.
//
// Configuration comes from the environment (see internal/config). The server
// binds loopback by default — Caddy is the only externally reachable listener.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"msb-manager/internal/config"
	"msb-manager/internal/lock"
	"msb-manager/internal/msb"
	"msb-manager/internal/server"
)

const shutdownTimeout = 15 * time.Second

// HTTP server timeouts. The listener is loopback-only with Caddy fronting it
// (CLAUDE.md invariant), but a slow upstream connection from Caddy still
// consumes a server goroutine, so we bound every phase rather than rely on the
// zero-value (infinite) defaults.
const (
	// readHeaderTimeout is the cheap, high-value Slowloris mitigation: headers
	// must arrive promptly or the connection is dropped before a handler runs.
	readHeaderTimeout = 5 * time.Second
	// readTimeout bounds reading the whole request including body. Specs and
	// volume/snapshot bodies are small declarative YAML/JSON (capped at 64 KiB),
	// so a generous few seconds is ample.
	readTimeout = 15 * time.Second
	// writeTimeout bounds writing the response. It must exceed the msb command
	// timeout (issue #4) so a slow lifecycle op can still write its 504 before
	// the connection is torn down; logs are fetch-only (buffered fully) so there
	// is no streaming deadline to conflict with.
	writeTimeout = 120 * time.Second
	// idleTimeout reaps keep-alive connections that go quiet between requests.
	idleTimeout = 60 * time.Second

	// msbCmdTimeoutCeiling is the upper bound any per-invocation msb command
	// timeout (issue #4) may be configured to — enforced at config load via
	// config.MaxCmdTimeout. writeTimeout is kept strictly above it so a
	// timed-out command can always surface its error to the client.
	msbCmdTimeoutCeiling = config.MaxCmdTimeout
)

// newHTTPServer builds the control-plane HTTP server with conservative
// timeouts on every request phase. Extracted so the timeout configuration is
// unit-testable without binding a socket.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// sandboxInspector is the slice of the msb adapter the startup reconcile
// needs. An interface so tests can drive the retry loop with a fake.
type sandboxInspector interface {
	List(ctx context.Context) ([]msb.Sandbox, error)
	Inspect(ctx context.Context, name string) (msb.SandboxDetail, error)
}

// Backoff bounds for the startup reconcile retry loop. The expected failure
// mode is msb being transiently slow or hung (CONTEXT verification #3), so
// retries start quick and cap low — msb usually recovers within seconds.
const (
	reconcileInitialBackoff = 1 * time.Second
	reconcileMaxBackoff     = 30 * time.Second
)

// reconcileVolumes builds the initial volume->sandbox map from msb's running
// sandboxes, and hands it to the lock as authoritative truth.
func reconcileVolumes(ctx context.Context, client sandboxInspector, vlock *lock.VolumeLock) error {
	sandboxes, err := client.List(ctx)
	if err != nil {
		return err
	}
	state := make(map[string]string)
	for _, sb := range sandboxes {
		if sb.Status != "Running" {
			continue
		}
		detail, err := client.Inspect(ctx, sb.Name)
		if err != nil {
			return err
		}
		for _, v := range detail.VolumeNames() {
			state[v] = sb.Name
		}
	}
	vlock.Reconcile(state)
	return nil
}

// reconcileVolumesWithRetry runs reconcileVolumes until it succeeds, backing
// off between attempts. FAIL-CLOSED DECISION (issue #20): the volume lock is
// the only guard against double-mounting a volume, so the server must never
// serve lock-guarded mutations with an unseeded lock. The listener is not
// started until this returns nil — mutations are gated by construction. The
// only other exit is ctx cancellation (a shutdown signal during the loop), in
// which case startup aborts with an error.
func reconcileVolumesWithRetry(ctx context.Context, client sandboxInspector, vlock *lock.VolumeLock, logger *slog.Logger, initialBackoff, maxBackoff time.Duration) error {
	backoff := initialBackoff
	for {
		err := reconcileVolumes(ctx, client, vlock)
		if err == nil {
			return nil
		}
		logger.Warn("startup volume reconcile failed; retrying before serving", "err", err, "backoff", backoff)
		// NewTimer + Stop rather than time.After so the timer goroutine is
		// released immediately when the shutdown branch wins, instead of
		// lingering until backoff elapses.
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("startup volume reconcile abandoned: %w (last reconcile error: %v)", ctx.Err(), err)
		case <-t.C:
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		// A shutdown signal during the startup reconcile loop surfaces as a
		// context.Canceled-wrapped error (issue #20 fail-closed path). That is
		// an operator-requested stop, not a failure: exit 0 so a systemd
		// Restart=on-failure unit lets the service stay stopped rather than
		// bouncing it.
		if errors.Is(err, context.Canceled) {
			logger.Info("startup aborted by shutdown signal")
			return
		}
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	msbClient := msb.NewClientWithTimeout(cfg.MsbPath, msb.ExecRunner{}, cfg.CmdTimeout)

	// Installed before the reconcile loop so a shutdown signal during a long
	// retry aborts startup rather than waiting for the next attempt.
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Seed the volume lock from msb's actual running state so a crash-restart
	// doesn't think every volume is free. Fail closed (issue #20): keep
	// retrying — never listen — until a reconcile succeeds, because the most
	// likely failure is msb being transiently hung (CONTEXT verification #3)
	// and serving with an empty lock would let a create/start double-mount
	// every volume held by a sandbox that was running before the restart.
	vlock := lock.New()
	if err := reconcileVolumesWithRetry(signalCtx, msbClient, vlock, logger,
		reconcileInitialBackoff, reconcileMaxBackoff); err != nil {
		return err
	}

	srv := newHTTPServer(
		cfg.ListenAddr,
		server.NewWithLock(server.Config{Token: cfg.Token}, msbClient, vlock),
	)

	// Run the listener in the background; main goroutine waits for a signal.
	listenErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
			return
		}
		listenErr <- nil
	}()

	select {
	case err := <-listenErr:
		return err
	case <-signalCtx.Done():
		logger.Info("shutdown signal received, draining")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-listenErr
}
