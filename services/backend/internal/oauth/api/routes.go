// Package api is the apigen source for the oauth module. It declares the HTTP
// surface as an annotated Go interface; apigen derives the service and
// repository interfaces from it. The handler is hand-written (apigen's generated
// handler bypasses httpx.RenderError and leaks wrapped causes), so no handler
// layer is generated from this file.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
)

// OAuthAPI is the OAuth connection surface: begin an authorization, complete the
// provider callback, list a user's connections, and disconnect a provider.
//
// The path parameter :provider is the public provider name ("google" for
// YouTube, "meta" for Instagram), not the internal platform key.
type OAuthAPI interface {
	// GET /oauth/:provider/authorize
	Authorize(ctx context.Context, provider string) (model.AuthorizeResponse, error)

	// GET /oauth/:provider/callback
	Callback(ctx context.Context, provider string) (model.ConnectionResponse, error)

	// GET /oauth/connections
	Connections(ctx context.Context) ([]model.ConnectionResponse, error)

	// DELETE /oauth/:provider
	Disconnect(ctx context.Context, provider string) error

	// POST /oauth/meta/deauthorize
	//
	// Meta's deauthorize callback: the person removed our app, so their consent is
	// gone and we erase their connection and everything derived from it. It is
	// UNAUTHENTICATED — Meta holds no session — and authenticates itself with a
	// signed_request HMAC'd with our app secret. Mounted by the composition root
	// (it cascades across the oauth and report modules); declared here so the
	// contract documents the route.
	MetaDeauthorize(ctx context.Context, body model.SignedRequest) error

	// POST /oauth/meta/data-deletion
	//
	// Meta's data-deletion-request callback. Same authentication (signed_request),
	// and it must return a confirmation code plus a URL where the person can check
	// the status of their request.
	MetaDataDeletion(ctx context.Context, body model.SignedRequest) (model.DataDeletionResponse, error)
}
