package service

import (
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/alerts/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestServiceIsNotImplemented(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.ListAlerts(ctx); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("ListAlerts err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.CreateAlert(ctx, model.CreateAlertRequest{}); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("CreateAlert err = %v, want ErrNotImplemented", err)
	}
	if err := s.DeleteAlert(ctx, "id"); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("DeleteAlert err = %v, want ErrNotImplemented", err)
	}
}
