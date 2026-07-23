package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgUniqueViolation is the SQLSTATE code PostgreSQL returns when a statement
// violates a unique constraint. On ml_canary_account it means the audit is already
// a canary for that model.
const pgUniqueViolation = "23505"

// notFound reports whether err is the pgx no-rows sentinel, returned when a query
// addressed a row that does not exist, including an UPDATE ... RETURNING whose
// WHERE clause matched nothing.
func notFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// uniqueViolation reports whether err is a unique-constraint violation, so the
// caller can render it as a conflict instead of a 500.
func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
