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
	"msb-manager/internal/msb"
	"msb-manager/internal/server"
)

const shutdownTimeout = 15 * time.Second

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
	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: server.New(server.Config{Token: cfg.Token}, msbClient),
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
