package service

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// signupProvider is the only provider the OAuth-as-signup funnel accepts:
// Instagram, via Meta. Signup is an Instagram-creator acquisition flow, so the
// provider is fixed here rather than taken from the path.
const signupProvider = ProviderMeta

// AuthorizeSignup begins an anonymous Meta authorization for the OAuth-as-signup
// flow. Unlike Authorize it takes no caller — there is no account yet. It
// validates the captured email, mints CSRF/PKCE state that carries the email and
// a signup marker, and returns the provider consent URL.
func (s *Service) AuthorizeSignup(ctx context.Context, email string) (model.AuthorizeResponse, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return model.AuthorizeResponse{}, err
	}

	meta, pc, err := s.resolveEnabled(signupProvider)
	if err != nil {
		return model.AuthorizeResponse{}, err
	}
	clientID, _, err := s.credentials(pc)
	if err != nil {
		return model.AuthorizeResponse{}, err
	}

	verifier, err := randomURLToken(32)
	if err != nil {
		return model.AuthorizeResponse{}, errs.Wrap(err, errs.KindInternal, "oauth.internal",
			"could not start authorization")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return model.AuthorizeResponse{}, errs.Wrap(err, errs.KindInternal, "oauth.internal",
			"could not start authorization")
	}

	data := model.StateData{
		Platform:     string(meta.platform),
		Provider:     signupProvider,
		CodeVerifier: verifier,
		Signup:       true,
		Email:        email,
	}
	if err := s.states.Save(ctx, state, data, s.cfg.stateTTL()); err != nil {
		return model.AuthorizeResponse{}, errs.Wrap(err, errs.KindUnavailable, "oauth.state_persist",
			"could not persist authorization state")
	}

	authURL, err := buildAuthorizeURL(meta, clientID, s.signupRedirectURI(), pc.Auth.Scopes, state, pkceChallenge(verifier))
	if err != nil {
		return model.AuthorizeResponse{}, errs.Wrap(err, errs.KindInternal, "oauth.internal",
			"could not build authorization url")
	}

	return model.AuthorizeResponse{AuthorizationURL: authURL, State: state}, nil
}

// CallbackSignup completes the anonymous authorization. It validates the
// single-use signup state, exchanges the code under PKCE, resolves the Meta
// identity and Instagram Business account, then provisions the account in one
// logical flow: find-or-create the user from the captured email, upsert the
// influencer for the connected account, seal and persist the token bound to that
// user, and mint a session. The returned AuthSession lets the web log the new
// account in.
//
// The steps run in order because each depends on the previous: the token is
// sealed under the user's id (so the user must exist first), and the session is
// issued last, only once the connection is durably stored. A failure at any step
// returns that step's error; nothing partial is reported as success.
func (s *Service) CallbackSignup(ctx context.Context, params CallbackParams) (model.AuthSession, error) {
	meta, pc, err := s.resolveEnabled(signupProvider)
	if err != nil {
		return model.AuthSession{}, err
	}

	if params.Error != "" {
		// The visitor declined consent or the provider rejected the request. The
		// provider's error text is not echoed back to avoid reflecting untrusted
		// content.
		return model.AuthSession{}, errs.New(errs.KindInvalid, "oauth.authorization_denied",
			"authorization was not granted")
	}
	if params.Code == "" || params.State == "" {
		return model.AuthSession{}, errs.New(errs.KindInvalid, "oauth.invalid_callback",
			"authorization response is missing required parameters")
	}

	data, ok, err := s.states.Consume(ctx, params.State)
	if err != nil {
		return model.AuthSession{}, errs.Wrap(err, errs.KindUnavailable, "oauth.state_lookup",
			"could not validate authorization state")
	}
	// An unknown/expired/reused state, or a connect-flow state replayed against the
	// signup endpoint (data.Signup false), or one for a different provider, is a
	// single indistinguishable rejection: revealing which failed would help an
	// attacker probe the store.
	if !ok || !data.Signup || data.Provider != signupProvider {
		return model.AuthSession{}, errs.New(errs.KindUnauthorized, "oauth.state_invalid",
			"authorization state is invalid or expired")
	}

	clientID, clientSecret, err := s.credentials(pc)
	if err != nil {
		return model.AuthSession{}, err
	}

	res, err := s.provider.Exchange(ctx, ExchangeRequest{
		Provider:       signupProvider,
		TokenURL:       meta.tokenURL,
		AccountInfoURL: pc.BaseURL + meta.accountPath,
		ClientID:       clientID,
		ClientSecret:   clientSecret,
		RedirectURI:    s.signupRedirectURI(),
		Code:           params.Code,
		CodeVerifier:   data.CodeVerifier,
	})
	if err != nil {
		return model.AuthSession{}, classifyExchangeError(err)
	}
	if res.AccessToken == "" || res.ProviderAccountID == "" {
		return model.AuthSession{}, errs.New(errs.KindUnavailable, "oauth.exchange_failed",
			"could not exchange the authorization code")
	}

	// The email is the identity we vouched for at authorize time; it is what the
	// account is created from. It never comes off the callback URL.
	userID, err := s.users.FindOrCreateUserByEmail(ctx, data.Email)
	if err != nil {
		return model.AuthSession{}, errs.Wrap(err, errs.KindUnavailable, "oauth.provision_user_failed",
			"could not create the account")
	}

	if _, err := s.influencers.UpsertInstagramInfluencer(ctx, InfluencerSignup{
		OwnerUserID:        userID,
		InstagramAccountID: res.ProviderAccountID,
		Handle:             res.ProviderAccountHandle,
		ProviderUserID:     res.ProviderUserID,
	}); err != nil {
		return model.AuthSession{}, errs.Wrap(err, errs.KindUnavailable, "oauth.provision_influencer_failed",
			"could not create the creator profile")
	}

	tok, err := s.sealToken(userID, meta.platform, res, pc.Auth.Scopes)
	if err != nil {
		return model.AuthSession{}, err
	}
	if err := s.tokens.Upsert(ctx, tok); err != nil {
		return model.AuthSession{}, errs.Wrap(err, errs.KindUnavailable, "oauth.persist_failed",
			"could not persist the connection")
	}

	session, err := s.sessions.IssueSession(ctx, userID)
	if err != nil {
		return model.AuthSession{}, errs.Wrap(err, errs.KindUnavailable, "oauth.session_failed",
			"could not establish a session")
	}
	return session, nil
}

// signupRedirectURI is the callback the provider redirects a signup back to. It
// is a DISTINCT path from the connect callback, and must match both the value
// sent at authorize time and the app's registered redirect URIs, or the token
// exchange is rejected.
func (s *Service) signupRedirectURI() string {
	return s.cfg.RedirectBaseURL + "/oauth/" + signupProvider + "/signup/callback"
}

// normalizeEmail validates and canonicalizes a captured signup email. An address
// that does not parse is a caller error, not a server fault, so it is rejected
// before any state is minted or the provider is contacted.
func normalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", errs.New(errs.KindInvalid, "oauth.email_required",
			"an email address is required to sign up")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", errs.New(errs.KindInvalid, "oauth.email_invalid",
			"enter a valid email address")
	}
	return addr.Address, nil
}

// classifyExchangeError maps a provider exchange failure to a domain error. A
// missing Instagram Business account is a distinct, recoverable condition — the
// person can link one — so it becomes a guided-fix KindInvalid error the web can
// act on, rather than a generic retryable exchange failure. Everything else is a
// transport/upstream fault.
func classifyExchangeError(err error) error {
	if errors.Is(err, ErrNoInstagramBusinessAccount) {
		return errs.New(errs.KindInvalid, "oauth.instagram_business_account_required",
			"connect an Instagram account set to Business or Creator and linked to a Facebook Page")
	}
	return errs.Wrap(err, errs.KindUnavailable, "oauth.exchange_failed",
		"could not exchange the authorization code")
}
