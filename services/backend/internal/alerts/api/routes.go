// Package api is the apigen source for the alerts module: an annotated Go
// interface from which the service interface is generated. The module lets a
// caller register threshold rules on an influencer's metrics and be notified when
// one is crossed. It is a scaffold — the service returns errs.ErrNotImplemented —
// so the shape exists and enabling it is a small change, but no route is mounted
// until the alerting engine is built.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/alerts/internal/model"
)

// AlertsAPI is the alerts module's HTTP surface. Every method takes
// context.Context first; the authenticated caller's identity travels on that
// context, so it never appears as a parameter.
type AlertsAPI interface {
	// GET /alerts
	ListAlerts(ctx context.Context) ([]model.AlertResponse, error)

	// POST /alerts
	CreateAlert(ctx context.Context, req model.CreateAlertRequest) (model.AlertResponse, error)

	// DELETE /alerts/:id
	DeleteAlert(ctx context.Context, id string) error
}
