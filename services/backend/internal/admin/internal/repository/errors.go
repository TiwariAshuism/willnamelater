package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// pgForeignKeyViolation is the SQLSTATE code PostgreSQL returns when a statement
// violates a foreign-key constraint. On a dispute insert it is the signal that
// the referenced audit_job does not exist.
const pgForeignKeyViolation = "23503"

// notFound reports whether err is the pgx no-rows sentinel returned when a query
// addressed a row that does not exist, including an UPDATE ... RETURNING whose
// WHERE clause matched nothing.
func notFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// isForeignKeyViolation reports whether err is a PostgreSQL foreign-key
// violation.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgForeignKeyViolation
}

// errAuditNotFound is returned when a dispute is filed against an audit that
// does not exist.
func errAuditNotFound() error {
	return errs.New(errs.KindNotFound, "admin.audit_not_found", "audit does not exist")
}

// errDisputeNotFound is the single not-found error for the dispute resource.
func errDisputeNotFound() error {
	return errs.New(errs.KindNotFound, "admin.dispute_not_found", "dispute does not exist")
}

// errAlreadyResolved is returned when a resolve targets a dispute that is no
// longer open.
func errAlreadyResolved() error {
	return errs.New(errs.KindConflict, "admin.dispute_already_resolved", "dispute is not open")
}
