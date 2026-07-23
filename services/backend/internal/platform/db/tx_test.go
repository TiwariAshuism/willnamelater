package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeTx records Commit/Rollback calls so InTx's lifecycle decisions can be
// asserted without a database. The embedded pgx.Tx is never populated; only
// Commit and Rollback are exercised by InTx.
type fakeTx struct {
	pgx.Tx
	commitErr   error
	rollbackErr error
	commits     int
	rollbacks   int
}

func (f *fakeTx) Commit(context.Context) error   { f.commits++; return f.commitErr }
func (f *fakeTx) Rollback(context.Context) error { f.rollbacks++; return f.rollbackErr }

// fakeBeginner hands out a prepared fakeTx, or an error if beginErr is set.
type fakeBeginner struct {
	tx       *fakeTx
	beginErr error
	begins   int
}

func (b *fakeBeginner) Begin(context.Context) (pgx.Tx, error) {
	b.begins++
	if b.beginErr != nil {
		return nil, b.beginErr
	}
	return b.tx, nil
}

func TestInTx(t *testing.T) {
	fnErr := errors.New("fn failed")
	commitErr := errors.New("commit failed")

	tests := []struct {
		name          string
		beginErr      error
		commitErr     error
		fn            func(pgx.Tx) error
		wantErrIs     error // when set, returned error must wrap this
		wantErrKind   *errs.Kind
		wantCommits   int
		wantRollbacks int
	}{
		{
			name:          "commit on success",
			fn:            func(pgx.Tx) error { return nil },
			wantCommits:   1,
			wantRollbacks: 0,
		},
		{
			name:          "rollback and surface fn error unchanged",
			fn:            func(pgx.Tx) error { return fnErr },
			wantErrIs:     fnErr,
			wantCommits:   0,
			wantRollbacks: 1,
		},
		{
			name:          "wrap begin failure as unavailable",
			beginErr:      errors.New("no connection"),
			fn:            func(pgx.Tx) error { return nil },
			wantErrKind:   kindPtr(errs.KindUnavailable),
			wantCommits:   0,
			wantRollbacks: 0,
		},
		{
			name:          "rollback and wrap commit failure",
			commitErr:     commitErr,
			fn:            func(pgx.Tx) error { return nil },
			wantErrKind:   kindPtr(errs.KindUnavailable),
			wantCommits:   1,
			wantRollbacks: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTx{commitErr: tt.commitErr}
			b := &fakeBeginner{tx: tx, beginErr: tt.beginErr}

			err := InTx(context.Background(), b, tt.fn)

			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("error = %v, want it to wrap %v", err, tt.wantErrIs)
			}
			if tt.wantErrKind != nil {
				if err == nil {
					t.Fatalf("error = nil, want kind %v", *tt.wantErrKind)
				}
				if got := errs.KindOf(err); got != *tt.wantErrKind {
					t.Fatalf("error kind = %v, want %v", got, *tt.wantErrKind)
				}
			}
			if tt.wantErrIs == nil && tt.wantErrKind == nil && err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if tx.commits != tt.wantCommits {
				t.Errorf("commits = %d, want %d", tx.commits, tt.wantCommits)
			}
			if tx.rollbacks != tt.wantRollbacks {
				t.Errorf("rollbacks = %d, want %d", tx.rollbacks, tt.wantRollbacks)
			}
		})
	}
}

// TestInTxRollsBackAndRepanicsOnPanic verifies a panic in fn rolls the
// transaction back and continues propagating rather than being swallowed.
func TestInTxRollsBackAndRepanicsOnPanic(t *testing.T) {
	tx := &fakeTx{}
	b := &fakeBeginner{tx: tx}
	panicValue := "boom"

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("InTx swallowed the panic; want it to re-panic")
		}
		if r != panicValue {
			t.Fatalf("recovered %v, want %v", r, panicValue)
		}
		if tx.rollbacks != 1 {
			t.Errorf("rollbacks = %d, want 1", tx.rollbacks)
		}
		if tx.commits != 0 {
			t.Errorf("commits = %d, want 0", tx.commits)
		}
	}()

	// The panic propagates out of InTx, so its error return is never observed.
	_ = InTx(context.Background(), b, func(pgx.Tx) error { panic(panicValue) })
}

func kindPtr(k errs.Kind) *errs.Kind { return &k }
