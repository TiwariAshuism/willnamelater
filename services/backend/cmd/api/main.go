// Command api serves the InfluAudit HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getnyx/influaudit/backend/internal/app"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
)

// shutdownGrace bounds how long in-flight requests have to finish once a
// termination signal arrives. Beyond this the process exits regardless, so a
// wedged handler cannot block a deploy indefinitely.
const shutdownGrace = 20 * time.Second

func main() {
	configPath := flag.String("config", "", "optional path to a YAML config file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		slog.Error("api exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// Signal handling is installed before Build so a slow boot (a database that
	// is still starting) can still be interrupted with Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeApp(a)

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           a.Router(),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening",
			slog.String("addr", cfg.HTTP.Addr),
			slog.String("env", string(cfg.Environment)),
			slog.String("version", app.Version),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining")
	}

	// A fresh context: ctx is already cancelled, and Shutdown needs a live one
	// to bound the drain.
	drainCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	return srv.Shutdown(drainCtx)
}

// closeApp tears the dependency graph down on a context that is not already
// cancelled, so exporters and pools get their full drain window.
func closeApp(a *app.App) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	if err := a.Close(ctx); err != nil {
		slog.Error("teardown failed", slog.Any("error", err))
	}
}
