// Package api is the apigen source for the admin module: an annotated Go
// interface from which the service interface is generated (apigen -layers
// service). The handler, repository, and service implementation are hand-written.
//
// The admin module owns the dispute table and the dispute-review loop, and reads
// two neighbouring modules — the llm layer's generation costs and the audit
// orchestrator's fraud estimates — plus the asynq queue state, only through
// consumer-side ports in internal/admin/port. Filing a dispute is open to any
// authenticated caller; every /admin route is admin-only, enforced in the
// service through the module's AdminGuard port.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/admin/internal/model"
)

// AdminAPI declares the admin module's HTTP endpoints. Every method takes
// context.Context first; the caller's identity (and, for the admin routes, their
// admin role) travels on that context and is resolved through the module's
// CallerID and AdminGuard ports, so it never appears as a parameter.
type AdminAPI interface {
	// POST /audits/:id/disputes
	FileDispute(ctx context.Context, id string, req model.FileDisputeRequest) (model.DisputeResponse, error)

	// GET /admin/disputes
	ListDisputeQueue(ctx context.Context) ([]model.DisputeResponse, error)

	// POST /admin/disputes/:id/resolve
	ResolveDispute(ctx context.Context, id string, req model.ResolveDisputeRequest) (model.DisputeResponse, error)

	// GET /admin/costs
	CostDashboard(ctx context.Context) (model.CostDashboardResponse, error)

	// GET /admin/queues
	QueueMonitor(ctx context.Context) (model.QueueMonitorResponse, error)

	// GET /admin/training/labels
	ExportLabels(ctx context.Context) (model.LabelExportResponse, error)
}
