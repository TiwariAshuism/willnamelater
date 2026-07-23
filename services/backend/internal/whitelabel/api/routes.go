// Package api is the apigen source for the whitelabel module: an annotated Go
// interface from which the service interface is generated. The module lets an
// agency read and update the branding applied to its reports and public badges.
// It is a scaffold — the service returns errs.ErrNotImplemented — so the shape
// exists and enabling it is a small change, but no route is mounted until
// white-label branding is built.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/model"
)

// WhitelabelAPI is the whitelabel module's HTTP surface. Every method takes
// context.Context first; the authenticated caller's identity travels on that
// context, so it never appears as a parameter.
type WhitelabelAPI interface {
	// GET /whitelabel
	GetWhitelabel(ctx context.Context) (model.WhitelabelResponse, error)

	// PUT /whitelabel
	UpdateWhitelabel(ctx context.Context, req model.UpdateWhitelabelRequest) (model.WhitelabelResponse, error)
}
