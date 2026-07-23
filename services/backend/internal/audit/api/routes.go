// Package api is the apigen source for the audit module: an annotated Go
// interface from which the service interface is generated. Only the service
// layer is generated (apigen -layers service); the handler, repository, and
// service implementation are hand-written.
//
// The audit module owns the audit_job and audit_platform_result tables and the
// orchestration behind them. These three routes are its entire HTTP surface; the
// audit:run background task that does the actual fetch/score/report work is a
// worker handler the module registers, not an HTTP route, and so is not declared
// here.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/model"
)

// AuditAPI declares the audit module's HTTP endpoints. Every method takes
// context.Context first; the authenticated caller's identity travels on that
// context (resolved through the module's CallerID port), so it never appears as
// a parameter.
type AuditAPI interface {
	// POST /audits
	SubmitAudit(ctx context.Context, req model.SubmitAuditRequest) (model.AuditResponse, error)

	// GET /audits/:id
	GetAudit(ctx context.Context, id string) (model.AuditResponse, error)

	// GET /audits
	ListAudits(ctx context.Context) ([]model.AuditResponse, error)
}
