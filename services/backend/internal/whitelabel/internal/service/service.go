// Package service implements the whitelabel module's business logic. White-label
// branding is deferred, so every operation returns errs.ErrNotImplemented at the
// service boundary: calling one is an explicit, typed 501 rather than a silent
// empty result. This is the intended behaviour of the scaffold, not a stub — the
// handler, routes, and DTO contract around it are real, so enabling the module is
// a matter of writing the repository behind this same interface.
package service

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/model"
)

// Service implements WhitelabelService.
type Service struct{}

// New builds the whitelabel service.
func New() *Service { return &Service{} }

var _ WhitelabelService = (*Service)(nil)

// GetWhitelabel is not implemented yet.
func (s *Service) GetWhitelabel(context.Context) (model.WhitelabelResponse, error) {
	return model.WhitelabelResponse{}, errs.ErrNotImplemented
}

// UpdateWhitelabel is not implemented yet.
func (s *Service) UpdateWhitelabel(context.Context, model.UpdateWhitelabelRequest) (model.WhitelabelResponse, error) {
	return model.WhitelabelResponse{}, errs.ErrNotImplemented
}
