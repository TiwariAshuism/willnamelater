package service

import (
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/campaign/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestServiceIsNotImplemented(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.CreateCampaign(ctx, model.CreateCampaignRequest{}); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("CreateCampaign err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.ListCampaigns(ctx); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("ListCampaigns err = %v, want ErrNotImplemented", err)
	}
	if _, err := s.GetCampaign(ctx, "id"); !errors.Is(err, errs.ErrNotImplemented) {
		t.Fatalf("GetCampaign err = %v, want ErrNotImplemented", err)
	}
}
