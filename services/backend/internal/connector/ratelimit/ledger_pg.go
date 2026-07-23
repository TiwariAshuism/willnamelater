package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// rowQuerier is the narrow slice of *db.Pool this repository needs. Depending on
// the interface rather than the concrete pool keeps the SQL layer small and
// makes the atomic statement the only thing under test here.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PGRepository is the Postgres-backed Repository for the quota-units ledger,
// storing one accounting row per (platform, UTC day) in api_quota_ledger.
type PGRepository struct {
	q rowQuerier
}

// NewPGRepository builds a PGRepository over the shared connection pool.
func NewPGRepository(pool *db.Pool) *PGRepository {
	return &PGRepository{q: pool}
}

var _ Repository = (*PGRepository)(nil)

// debitSQL performs the atomic check-and-increment. The INSERT seeds a fresh row
// for a new day; on the second and later debits of the day it conflicts on the
// (platform, day) primary key and takes the UPDATE branch, which increments
// calls_made only when the guard holds. When the guard is false the statement
// matches no row and RETURNING yields nothing, surfacing as pgx.ErrNoRows — the
// signal that the debit was rejected. Because the guard is evaluated and the
// increment applied within one statement, holding the row lock, two concurrent
// debits are serialized and cannot jointly overrun quota_limit.
//
// The guard and the stored quota_limit both use EXCLUDED.quota_limit (the value
// supplied by the caller from config) so the config remains the single source of
// truth for the budget and a stale or NULL quota_limit self-heals on the next
// debit. The fresh-INSERT branch is intentionally unguarded; the service layer
// guarantees units <= limit before calling, so the first debit of a day can
// never seed a row already over budget.
const debitSQL = `
INSERT INTO api_quota_ledger AS l (platform, day, calls_made, quota_limit)
VALUES ($1, $2, $3, $4)
ON CONFLICT (platform, day) DO UPDATE
   SET calls_made  = l.calls_made + EXCLUDED.calls_made,
       quota_limit = EXCLUDED.quota_limit
   WHERE l.calls_made + EXCLUDED.calls_made <= EXCLUDED.quota_limit
RETURNING calls_made`

const usedSQL = `SELECT calls_made FROM api_quota_ledger WHERE platform = $1 AND day = $2`

// Debit implements Repository.Debit with the single conditional UPDATE above.
func (r *PGRepository) Debit(ctx context.Context, platform string, day time.Time, units, limit int) (int, bool, error) {
	var used int
	err := r.q.QueryRow(ctx, debitSQL, platform, day, units, limit).Scan(&used)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Guard failed: the debit would have exceeded the limit, so no row
			// was written. This is a rejection, not a data-access failure.
			return 0, false, nil
		}
		return 0, false, err
	}
	return used, true, nil
}

// Used implements Repository.Used, returning 0 when the day has no row yet.
func (r *PGRepository) Used(ctx context.Context, platform string, day time.Time) (int, error) {
	var used int
	err := r.q.QueryRow(ctx, usedSQL, platform, day).Scan(&used)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return used, nil
}
