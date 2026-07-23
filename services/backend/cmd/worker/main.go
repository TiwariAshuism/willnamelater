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

// schedulerUniqueTTL bounds the uniqueness lock on a scheduled enqueue, so two
// scheduler instances firing the same cron tick enqueue the task once. It is
// shorter than the smallest schedule interval (nightly).
const schedulerUniqueTTL = 2 * time.Hour

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

	// One broker connection for the server, the scheduler, and (in the API
	// process) the enqueuing client. app.RedisOpt is the only place these settings
	// are derived, so the queue this worker consumes from is by construction the
	// queue the API enqueues onto — including its TLS setting, which every managed
	// Redis requires.
	redisOpt := app.RedisOpt(cfg)

	srv := asynq.NewServer(
		redisOpt,
		asynq.Config{
			Concurrency: concurrency,
			// Audits are long-running and expensive in third-party quota, so a
			// task that panics must not be silently retried into the ground.
			ShutdownTimeout: shutdownGrace,
			Logger:          asynqLogger{},
		},
	)

	// Task handlers are registered by the modules that own them.
	mux := asynq.NewServeMux()
	a.RegisterTasks(mux)

	// The scheduler enqueues the app's periodic tasks (the nightly corpus
	// recompute) on their cron cadence; the server above consumes them. They are
	// separate asynq components with separate lifecycles, so both are run and
	// drained here.
	scheduler := asynq.NewScheduler(redisOpt, &asynq.SchedulerOpts{
		Logger:   asynqLogger{},
		Location: time.UTC,
	})
	for _, entry := range a.ScheduledTasks() {
		// Unique across the cron interval so overlapping scheduler instances (a
		// rolling deploy running two worker replicas) enqueue the run once, not
		// twice. The handler is idempotent regardless.
		if _, err := scheduler.Register(entry.Cronspec, entry.Task, asynq.Unique(schedulerUniqueTTL)); err != nil {
			return err
		}
	}

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
	go func() {
		if err := scheduler.Run(); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining in-flight tasks")
		scheduler.Shutdown()
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
