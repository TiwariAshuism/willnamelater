package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// pgUniqueViolation is the SQLSTATE code PostgreSQL returns when a statement
// violates a unique constraint. It is the signal that a (platform, handle) pair
// already exists.
const pgUniqueViolation = "23505"

// handleConflictConstraints maps the unique constraints on influencer_handle to
// the client-facing conflict message they imply. Mapping by constraint name
// keeps the message specific without exposing raw database detail.
var handleConflictConstraints = map[string]string{
	"influencer_handle_platform_handle_key": "a handle with this platform and name already exists",
	"influencer_handle_platform_user_key":   "a handle with this platform and platform user id already exists",
}

// mapHandleWriteError translates a database write error on influencer_handle
// into a domain error. A unique-constraint violation becomes errs.KindConflict
// (rendered 409) rather than reaching the client as a generic 500; every other
// error is wrapped opaquely so its cause never leaks.
func mapHandleWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		message, ok := handleConflictConstraints[pgErr.ConstraintName]
		if !ok {
			message = "handle violates a uniqueness constraint"
		}
		return errs.Wrap(err, errs.KindConflict, "influencer.handle_conflict", message)
	}
	return errs.Wrap(err, errs.KindInternal, "influencer.handle_write_failed", "could not persist handle")
}

// notFound reports whether err is the pgx no-rows sentinel returned when a query
// addressed a row that does not exist.
func notFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
