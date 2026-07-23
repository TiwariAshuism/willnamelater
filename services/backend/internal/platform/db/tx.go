package db

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Beginner starts a transaction. *pgxpool.Pool satisfies it, and it is small
// enough to fake in tests so transaction semantics can be exercised without a
// live database.
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTx runs fn inside a single transaction begun from b.
//
// Exactly one of two things happens to the transaction: it is committed (fn
// returned nil and Commit succeeded) or it is rolled back (fn returned an error,
// fn panicked, or Commit failed). A deferred rollback guards every non-commit
// exit; after a successful commit the deferred rollback is skipped, so the
// transaction is never both committed and rolled back.
//
// The error from fn is returned unchanged and is never masked by the rollback's
// own error. A panic in fn is rolled back and then allowed to keep propagating.
func InTx(ctx context.Context, b Beginner, fn func(pgx.Tx) error) error {
	tx, err := b.Begin(ctx)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db.tx_begin", "could not begin transaction")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// Best-effort undo on the error and panic paths. On the failed-commit
		// path the transaction is already closed and Rollback reports
		// pgx.ErrTxClosed; either way the rollback's outcome must not override
		// the reason we are unwinding, so it is deliberately discarded. Not
		// recovering here lets a panic in fn continue propagating after the
		// transaction has been rolled back.
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "db.tx_commit", "could not commit transaction")
	}
	committed = true

	return nil
}
