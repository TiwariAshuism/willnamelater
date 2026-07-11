// Package api is the openapigen source for the report module: an annotated Go
// interface from which the OpenAPI contract for the report endpoints is derived.
//
// Unlike the modules that generate their service layer with apigen, the report
// module's handler and service are hand-written; this interface exists solely so
// openapigen discovers the two report routes and reflects their response shapes.
// The report is the deliverable of a specific audit, so both routes hang off the
// audit resource.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/report/internal/render"
)

// ReportAPI declares the report module's HTTP endpoints. Each method takes
// context.Context first; the authenticated caller's identity travels on that
// context and the audit read is scoped to it, so it never appears as a
// parameter.
type ReportAPI interface {
	// GET /audits/:id/report
	GetReport(ctx context.Context, id string) (render.Report, error)

	// GET /audits/:id/report.pdf
	GetReportPDF(ctx context.Context, id string) ([]byte, error)

	// POST /audits/:id/report/publish
	PublishReport(ctx context.Context, id string) (render.PublishResult, error)

	// GET /reports/:slug
	GetPublicBadge(ctx context.Context, slug string) (render.PublicBadge, error)
}
