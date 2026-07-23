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

// MigrateResult reports what a call to Up actually did.
//
// Changed distinguishes "applied pending migrations" from "already current".
// Reporting only success conflates the two, which hides a whole class of
// deployment bug: a container built from a stale image finds no migrations to
// apply and reports success, while the schema silently lags the code.
type MigrateResult struct {
	// Version is the schema version after the run.
	Version uint
	// Dirty is true when a previous run failed midway and left the schema in an
	// indeterminate state. It requires manual intervention.
	Dirty bool
	// Changed is false when the database was already at the latest version.
	Changed bool
}

// Up applies every pending migration found in migrationsDir to the database at
// dsn, in version order. migrationsDir is a local filesystem path; dsn is a
// postgres:// URL understood by the golang-migrate postgres driver.
//
// A database already at the latest version reports migrate.ErrNoChange, which
// Up treats as success with Changed=false rather than as an error.
func Up(dsn, migrationsDir string) (MigrateResult, error) {
	// The file source expects a URL; ToSlash keeps Windows paths valid.
	source := "file://" + filepath.ToSlash(migrationsDir)

	m, err := migrate.New(source, dsn)
	if err != nil {
		return MigrateResult{}, errs.Wrap(err, errs.KindUnavailable, "db.migrate_init",
			"could not initialise database migrations")
	}
	defer func() { _, _ = m.Close() }()

	changed := true
	if err := m.Up(); err != nil {
		if !errors.Is(err, migrate.ErrNoChange) {
			return MigrateResult{}, errs.Wrap(err, errs.KindInternal, "db.migrate_up",
				"could not apply database migrations")
		}
		changed = false
	}

	// A fresh database that has never been migrated reports ErrNilVersion. That
	// can only happen if migrationsDir held no migrations, which is itself worth
	// surfacing rather than reporting a successful no-op.
	version, dirty, err := m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			return MigrateResult{}, errs.Wrap(err, errs.KindInvalid, "db.migrate_empty",
				"no migrations were found to apply")
		}
		return MigrateResult{}, errs.Wrap(err, errs.KindInternal, "db.migrate_version",
			"could not read the schema version")
	}

	return MigrateResult{Version: version, Dirty: dirty, Changed: changed}, nil
}
