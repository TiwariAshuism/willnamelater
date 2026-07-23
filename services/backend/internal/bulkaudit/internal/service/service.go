// Package service implements the bulkaudit module's business logic. The batch
// orchestrator is deferred, so every operation returns errs.ErrNotImplemented at
// the service boundary: calling one is an explicit, typed 501 rather than a silent
// empty result. This is the intended behaviour of the scaffold, not a stub — the
// handler, routes, and DTO contract around it are real, so enabling the module is
// a matter of writing the repository and the fan-out orchestration behind this
// same interface.
package service

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/bulkaudit/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Service implements BulkAuditService.
type Service struct{}

// New builds the bulkaudit service.
func New() *Service { return &Service{} }

var _ BulkAuditService = (*Service)(nil)

// CreateBulkAudit is not implemented yet.
func (s *Service) CreateBulkAudit(context.Context, model.CreateBulkAuditRequest) (model.BulkAuditResponse, error) {
	return model.BulkAuditResponse{}, errs.ErrNotImplemented
}

// ListBulkAudits is not implemented yet.
func (s *Service) ListBulkAudits(context.Context) ([]model.BulkAuditResponse, error) {
	return nil, errs.ErrNotImplemented
}

// GetBulkAudit is not implemented yet.
func (s *Service) GetBulkAudit(context.Context, string) (model.BulkAuditResponse, error) {
	return model.BulkAuditResponse{}, errs.ErrNotImplemented
}
