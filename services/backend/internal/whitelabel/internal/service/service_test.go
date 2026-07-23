package service

import (
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/model"
)

func TestServiceIsNotImplemented(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.GetWhitelabel(ctx); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("GetWhitelabel err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.UpdateWhitelabel(ctx, model.UpdateWhitelabelRequest{}); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("UpdateWhitelabel err = %v, want ErrNotImplemented", err)
	}
}
