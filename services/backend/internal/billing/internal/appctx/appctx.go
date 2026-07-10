// Package appctx carries the authenticated caller's identity on the request
// context within the billing module. The HTTP handler puts the user id on the
// context; the service and repository read it. Endpoints such as ListPlans and
// GetSubscription have no user parameter in their generated signatures, so the
// identity travels here rather than as an argument.
package appctx

import (
	"context"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// ctxKey is unexported so no other package can collide with the value stored
// under it.
type ctxKey int

const userIDKey ctxKey = iota

// WithUserID returns a copy of ctx that carries the authenticated user id.
func WithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserID returns the authenticated user id previously stored with WithUserID.
// It returns an unauthorized domain error when no identity is present, so a
// request that reaches business logic without authentication fails closed.
func UserID(ctx context.Context) (uuid.UUID, error) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, errs.New(errs.KindUnauthorized, "billing.unauthenticated", "authentication required")
	}
	return id, nil
}
