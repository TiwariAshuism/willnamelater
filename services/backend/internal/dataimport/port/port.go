// Package port declares the consumer-side interfaces the dataimport module
// depends on. The module never imports the auth module; the composition root
// adapts auth's context accessor onto Identity here.
package port

import (
	"context"

	"github.com/google/uuid"
)

// Identity resolves the authenticated caller from the request context. An upload
// is always owned by a caller, so every import is stored against the id this
// returns. It returns an unauthorized domain error when the request carried no
// authenticated identity.
type Identity interface {
	CallerID(ctx context.Context) (uuid.UUID, error)
}
