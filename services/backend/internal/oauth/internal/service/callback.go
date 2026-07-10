// Package service implements the oauth module's business logic: building the
// provider consent URL, exchanging the authorization code, and sealing the
// resulting tokens before they reach the database.
//
// The OAuth 'state' parameter is single-use, expiring, and bound to the user, so
// a replayed or forged callback is rejected. Provider scopes are read from the
// connector configuration and are never hardcoded here.
package service

import "context"

// CallbackParams are the OAuth callback query parameters. apigen's interface can
// express only the context and path parameters, not query parameters, so the
// hand-written handler parses these off the request and carries them to the
// service on the context via WithCallbackParams. The service reads them with
// callbackParamsFrom.
type CallbackParams struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

type callbackKey struct{}

// WithCallbackParams returns a context carrying the OAuth callback parameters.
// The oauth handler calls it; the service consumes them in Callback.
func WithCallbackParams(ctx context.Context, p CallbackParams) context.Context {
	return context.WithValue(ctx, callbackKey{}, p)
}

func callbackParamsFrom(ctx context.Context) (CallbackParams, bool) {
	p, ok := ctx.Value(callbackKey{}).(CallbackParams)
	return p, ok
}
