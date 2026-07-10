// Command migrate applies the versioned SQL migrations and exits.
//
// It runs as a one-shot container that the API and worker depend on, so the
// schema is always at least as new as the code that reads it.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

func main() {
	configPath := flag.String("config", "", "optional path to a YAML config file")
	dir := flag.String("dir", "migrations", "directory holding the migration files")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config", slog.Any("error", err))
		os.Exit(1)
	}

	if err := db.Up(cfg.Postgres.DSN.Reveal(), *dir); err != nil {
		slog.Error("migrate up", slog.Any("error", err))
		os.Exit(1)
	}

	slog.Info("migrations applied", slog.String("dir", *dir))
}
