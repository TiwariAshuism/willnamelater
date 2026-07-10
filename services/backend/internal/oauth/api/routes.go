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
}
