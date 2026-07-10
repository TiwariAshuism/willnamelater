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

	result, err := db.Up(cfg.Postgres.DSN.Reveal(), *dir)
	if err != nil {
		slog.Error("migrate up", slog.Any("error", err))
		os.Exit(1)
	}

	// A dirty schema means a previous run failed partway. Continuing would let
	// the API start against a half-applied schema.
	if result.Dirty {
		slog.Error("schema is dirty; a previous migration failed partway",
			slog.Uint64("version", uint64(result.Version)))
		os.Exit(1)
	}

	// Log the resulting version, and whether anything actually changed. A run
	// that reports success while applying nothing is how a stale image hides a
	// schema that lags the code.
	slog.Info("migrations up to date",
		slog.String("dir", *dir),
		slog.Uint64("version", uint64(result.Version)),
		slog.Bool("changed", result.Changed),
	)
}
