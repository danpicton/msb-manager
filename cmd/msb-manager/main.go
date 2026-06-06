// Command msb-manager runs the control-plane HTTP server.
//
// Configuration comes from the environment (see internal/config). The server
// binds loopback by default — Caddy is the only externally reachable listener.
package main

import (
	"context"
	"errors"
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

// reconcileVolumes builds the initial volume->sandbox map from msb's running
// sandboxes, and hands it to the lock as authoritative truth.
func reconcileVolumes(ctx context.Context, client *msb.Client, vlock *lock.VolumeLock) error {
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

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	msbClient := msb.NewClient(cfg.MsbPath, msb.ExecRunner{})

	// Seed the volume lock from msb's actual running state so a crash-restart
	// doesn't briefly think every volume is free.
	vlock := lock.New()
	if err := reconcileVolumes(context.Background(), msbClient, vlock); err != nil {
		// Don't fail to start — log and continue with an empty lock. A
		// reconcile failure usually means msb is down, in which case lifecycle
		// calls will fail anyway with a clearer error.
		logger.Warn("startup volume reconcile failed; lock starts empty", "err", err)
	}

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: server.NewWithLock(server.Config{Token: cfg.Token}, msbClient, vlock),
	}

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

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
