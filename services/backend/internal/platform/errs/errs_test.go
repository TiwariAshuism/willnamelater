package errs

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil error is internal", nil, http.StatusInternalServerError},
		{"foreign error is internal", errors.New("boom"), http.StatusInternalServerError},
		{"invalid", New(KindInvalid, "c", "m"), http.StatusBadRequest},
		{"unauthorized", New(KindUnauthorized, "c", "m"), http.StatusUnauthorized},
		{"forbidden", New(KindForbidden, "c", "m"), http.StatusForbidden},
		{"not found", New(KindNotFound, "c", "m"), http.StatusNotFound},
		{"conflict", New(KindConflict, "c", "m"), http.StatusConflict},
		{"quota exceeded", New(KindQuotaExceeded, "c", "m"), http.StatusPaymentRequired},
		{"rate limited", New(KindRateLimited, "c", "m"), http.StatusTooManyRequests},
		{"unavailable", New(KindUnavailable, "c", "m"), http.StatusServiceUnavailable},
		{"not implemented", ErrNotImplemented, http.StatusNotImplemented},
		{"wrapped domain error keeps kind", fmt.Errorf("ctx: %w", New(KindNotFound, "c", "m")), http.StatusNotFound},
		{"domain error wrapping foreign cause", Wrap(sql.ErrNoRows, KindNotFound, "c", "m"), http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Status(tt.err); got != tt.want {
				t.Errorf("Status() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUnwrapExposesCause(t *testing.T) {
	err := Wrap(sql.ErrNoRows, KindNotFound, "influencer.not_found", "no such influencer")

	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("errors.Is could not reach the wrapped cause")
	}

	// The cause must never leak into the client-facing message.
	if err.Message == sql.ErrNoRows.Error() {
		t.Error("cause leaked into the client-facing Message")
	}
}

func TestKindOfDefaultsToInternal(t *testing.T) {
	if got := KindOf(errors.New("unclassified")); got != KindInternal {
		t.Errorf("KindOf(foreign) = %v, want KindInternal", got)
	}
}
