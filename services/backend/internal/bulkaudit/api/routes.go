// Package api is the apigen source for the bulkaudit module: an annotated Go
// interface from which the service interface is generated. The module submits a
// batch of handles as one audit job and reports the batch's progress. It is a
// scaffold — the service returns errs.ErrNotImplemented — so the shape exists and
// enabling it is a small change, but no route is mounted until the batch
// orchestrator is built.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/bulkaudit/internal/model"
)

// BulkAuditAPI is the bulkaudit module's HTTP surface. Every method takes
// context.Context first; the authenticated caller's identity travels on that
// context, so it never appears as a parameter.
type BulkAuditAPI interface {
	// POST /bulk-audits
	CreateBulkAudit(ctx context.Context, req model.CreateBulkAuditRequest) (model.BulkAuditResponse, error)

	// GET /bulk-audits
	ListBulkAudits(ctx context.Context) ([]model.BulkAuditResponse, error)

	// GET /bulk-audits/:id
	GetBulkAudit(ctx context.Context, id string) (model.BulkAuditResponse, error)
}
