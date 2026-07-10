// Command worker consumes background jobs — principally the audit pipeline —
// from the Redis-backed asynq queue.
//
// It builds the identical dependency graph as cmd/api, so a task handler and an
// HTTP handler can never diverge in how they reach the database or a connector.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/app"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
)

const shutdownGrace = 30 * time.Second

// concurrency caps simultaneously-executing tasks. Audits are dominated by
// waiting on rate-limited third-party APIs rather than by CPU, so this is set
// above core count on purpose.
const concurrency = 16

func main() {
	configPath := flag.String("config", "", "optional path to a YAML config file")
	flag.Parse()

	if err := run(*configPath); err != nil {
		slog.Error("worker exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeApp(a)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password.Reveal(),
			DB:       cfg.Redis.DB,
		},
		asynq.Config{
			Concurrency: concurrency,
			// Audits are long-running and expensive in third-party quota, so a
			// task that panics must not be silently retried into the ground.
			ShutdownTimeout: shutdownGrace,
			Logger:          asynqLogger{},
		},
	)

	// Task handlers are registered by the modules that own them. An empty mux
	// consumes nothing, which is correct until the audit module lands.
	mux := asynq.NewServeMux()
	a.RegisterTasks(mux)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("worker started",
			slog.String("env", string(cfg.Environment)),
			slog.String("version", app.Version),
			slog.Int("concurrency", concurrency),
		)
		if err := srv.Run(mux); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining in-flight tasks")
		srv.Shutdown()
		return nil
	}
}

func closeApp(a *app.App) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	if err := a.Close(ctx); err != nil {
		slog.Error("teardown failed", slog.Any("error", err))
	}
}

// asynqLogger adapts asynq's logging interface onto slog so worker output is
// structured and correlates with the rest of the system.
type asynqLogger struct{}

func (asynqLogger) Debug(args ...any) { slog.Debug("asynq", slog.Any("args", args)) }
func (asynqLogger) Info(args ...any)  { slog.Info("asynq", slog.Any("args", args)) }
func (asynqLogger) Warn(args ...any)  { slog.Warn("asynq", slog.Any("args", args)) }
func (asynqLogger) Error(args ...any) { slog.Error("asynq", slog.Any("args", args)) }
func (asynqLogger) Fatal(args ...any) { slog.Error("asynq fatal", slog.Any("args", args)) }
