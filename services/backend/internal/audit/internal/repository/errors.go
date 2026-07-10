package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// notFound reports whether err is the pgx no-rows sentinel returned when a query
// addressed a row that does not exist. On an INSERT ... ON CONFLICT DO NOTHING
// it also signals that the row already existed and nothing was returned.
func notFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// errJobNotFound is the single not-found error for the audit_job resource, kept
// here so the code and message stay identical across the read methods.
func errJobNotFound() error {
	return errs.New(errs.KindNotFound, "audit.not_found", "audit does not exist")
}
