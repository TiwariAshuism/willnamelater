package db

import (
	"errors"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	// database/postgres registers the "postgres" driver and source/file
	// registers the "file" source; both are selected by URL scheme below.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Up applies every pending migration found in migrationsDir to the database at
// dsn, in version order. migrationsDir is a local filesystem path; dsn is a
// postgres:// URL understood by the golang-migrate postgres driver.
//
// A database already at the latest version reports migrate.ErrNoChange, which
// Up treats as success so that a no-op run is not an error.
func Up(dsn, migrationsDir string) error {
	// The file source expects a URL; ToSlash keeps Windows paths valid.
	source := "file://" + filepath.ToSlash(migrationsDir)

	m, err := migrate.New(source, dsn)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db.migrate_init", "could not initialise database migrations")
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return errs.Wrap(err, errs.KindInternal, "db.migrate_up", "could not apply database migrations")
	}

	return nil
}
