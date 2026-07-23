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

	// POST /oauth/meta/signup/start
	//
	// PUBLIC. Begins the anonymous OAuth-as-signup flow: a visitor with no account
	// supplies only an email and receives the Meta consent URL. The account is
	// created on the callback from the captured email plus the resolved Meta
	// identity. Unauthenticated by design — there is no user yet. The handler is
	// hand-written (query/session concerns apigen cannot express); declared here so
	// the contract documents the route.
	AuthorizeSignup(ctx context.Context, body model.SignupStartRequest) (model.AuthorizeResponse, error)

	// GET /oauth/meta/signup/callback
	//
	// PUBLIC. Completes the anonymous authorization: exchanges the code, provisions
	// the user + influencer + connection, and returns a session (access + refresh
	// tokens) so the web can log the new account in. A Meta login with no linked
	// Instagram Business account is rejected with a distinct, guided-fix error
	// (oauth.instagram_business_account_required) rather than a generic failure.
	CallbackSignup(ctx context.Context) (model.AuthSession, error)

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
