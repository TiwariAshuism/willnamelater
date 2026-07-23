package service

import (
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/bulkaudit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestServiceIsNotImplemented(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.CreateBulkAudit(ctx, model.CreateBulkAuditRequest{}); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("CreateBulkAudit err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.ListBulkAudits(ctx); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("ListBulkAudits err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.GetBulkAudit(ctx, "id"); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("GetBulkAudit err = %v, want ErrNotImplemented", err)
	}
}
