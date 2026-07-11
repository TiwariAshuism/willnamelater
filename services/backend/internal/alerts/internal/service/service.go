// Package service implements the alerts module's business logic. The alerting
// engine is deferred, so every operation returns errs.ErrNotImplemented at the
// service boundary: calling one is an explicit, typed 501 rather than a silent
// empty result. This is the intended behaviour of the scaffold, not a stub — the
// handler, routes, and DTO contract around it are real, so enabling the module is
// a matter of writing the repository and the rule-evaluation logic behind this
// same interface.
package service

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/alerts/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Service implements AlertsService.
type Service struct{}

// New builds the alerts service.
func New() *Service { return &Service{} }

var _ AlertsService = (*Service)(nil)

// ListAlerts is not implemented yet.
func (s *Service) ListAlerts(context.Context) ([]model.AlertResponse, error) {
	return nil, errs.ErrNotImplemented
}

// CreateAlert is not implemented yet.
func (s *Service) CreateAlert(context.Context, model.CreateAlertRequest) (model.AlertResponse, error) {
	return model.AlertResponse{}, errs.ErrNotImplemented
}

// DeleteAlert is not implemented yet.
func (s *Service) DeleteAlert(context.Context, string) error {
	return errs.ErrNotImplemented
}
