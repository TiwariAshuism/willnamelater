// Package authctx carries the authenticated caller's identity on a request
// context. The auth middleware places an Identity after verifying an access
// token; the service reads it to answer "who is calling" without re-parsing the
// token. Keeping the key unexported prevents any other package from forging an
// identity by writing the same context value.
package authctx

import (
	"context"

	"github.com/google/uuid"
)

// Identity is the authenticated caller established from a verified access token.
type Identity struct {
	UserID uuid.UUID
	Role   string
}

// contextKey is unexported so no other package can collide with the value this
// package stores on a context.
type contextKey int

const identityKey contextKey = iota

// With returns a copy of ctx carrying id.
func With(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// From returns the Identity stored by With, and false when the context carries
// none (the request was not authenticated).
func From(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)
	return id, ok
}
