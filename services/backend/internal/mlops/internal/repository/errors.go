package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// notFound reports whether err is the pgx no-rows sentinel, returned when a query
// addressed a row that does not exist, including an UPDATE ... RETURNING whose
// WHERE clause matched nothing.
func notFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
