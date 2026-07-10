package repository

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestMapHandleWriteError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantKind     errs.Kind
		wantContains string
	}{
		{
			name:         "unique on platform+handle is a conflict",
			err:          &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "influencer_handle_platform_handle_key"},
			wantKind:     errs.KindConflict,
			wantContains: "platform and name",
		},
		{
			name:         "unique on platform+user id is a conflict",
			err:          &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "influencer_handle_platform_user_key"},
			wantKind:     errs.KindConflict,
			wantContains: "platform user id",
		},
		{
			name:     "unknown unique constraint is still a conflict",
			err:      &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: "some_other_key"},
			wantKind: errs.KindConflict,
		},
		{
			name:     "non-unique pg error is internal",
			err:      &pgconn.PgError{Code: "23503"},
			wantKind: errs.KindInternal,
		},
		{
			name:     "plain error is internal",
			err:      errors.New("connection reset"),
			wantKind: errs.KindInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mapHandleWriteError(tt.err)

			if errs.KindOf(got) != tt.wantKind {
				t.Fatalf("kind = %v, want %v", errs.KindOf(got), tt.wantKind)
			}

			var domain *errs.Error
			if !errors.As(got, &domain) {
				t.Fatalf("expected an *errs.Error, got %T", got)
			}
			if tt.wantContains != "" && !strings.Contains(domain.Message, tt.wantContains) {
				t.Fatalf("message %q does not contain %q", domain.Message, tt.wantContains)
			}
			// The original cause must remain unwrappable for logs while never
			// being what classifies the error to the client.
			if !errors.Is(got, tt.err) {
				t.Fatalf("mapped error lost its cause: %v", got)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	t.Parallel()

	if !notFound(pgx.ErrNoRows) {
		t.Fatal("notFound(pgx.ErrNoRows) = false, want true")
	}
	if notFound(fmt.Errorf("wrapped: %w", pgx.ErrNoRows)) != true {
		t.Fatal("notFound should unwrap to pgx.ErrNoRows")
	}
	if notFound(errors.New("other")) {
		t.Fatal("notFound(other) = true, want false")
	}
}
