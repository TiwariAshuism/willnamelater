// Package service implements the campaign module's business logic. Campaign
// management is deferred, so every operation returns errs.ErrNotImplemented at the
// service boundary: calling one is an explicit, typed 501 rather than a silent
// empty result. This is the intended behaviour of the scaffold, not a stub — the
// handler, routes, and DTO contract around it are real, so enabling the module is
// a matter of writing the repository behind this same interface.
package service

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/campaign/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Service implements CampaignService.
type Service struct{}

// New builds the campaign service.
func New() *Service { return &Service{} }

var _ CampaignService = (*Service)(nil)

// CreateCampaign is not implemented yet.
func (s *Service) CreateCampaign(context.Context, model.CreateCampaignRequest) (model.CampaignResponse, error) {
	return model.CampaignResponse{}, errs.ErrNotImplemented
}

// ListCampaigns is not implemented yet.
func (s *Service) ListCampaigns(context.Context) ([]model.CampaignResponse, error) {
	return nil, errs.ErrNotImplemented
}

// GetCampaign is not implemented yet.
func (s *Service) GetCampaign(context.Context, string) (model.CampaignResponse, error) {
	return model.CampaignResponse{}, errs.ErrNotImplemented
}
