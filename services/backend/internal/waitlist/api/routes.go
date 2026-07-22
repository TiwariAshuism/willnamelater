// Package api is the apigen source for the waitlist module. The WaitlistAPI
// interface is the single declaration of the module's HTTP surface; the OpenAPI
// generator reflects it into the committed spec.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/model"
)

// WaitlistAPI declares the waitlist module's HTTP surface: a single public
// email-capture endpoint backing both the connect-wall return path (story B3) and
// the media-kit waitlist (story F1).
type WaitlistAPI interface {
	// POST /waitlist
	//
	// PUBLIC. Captures an email for a named source (connect_wall | mediakit).
	// Idempotent on (email, source): re-submitting the same pair is a no-op.
	Capture(ctx context.Context, req model.CaptureRequest) error
}
