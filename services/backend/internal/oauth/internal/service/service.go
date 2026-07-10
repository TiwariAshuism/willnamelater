package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Service implements OAuthService. It owns the CSRF/PKCE flow, envelope-seals
// issued tokens bound to their owner, and persists them through TokenStore. It
// holds no plaintext credential beyond the moment of sealing.
type Service struct {
	cfg       Config
	platforms map[connector.Platform]connector.PlatformConfig
	identity  Identity
	states    StateStore
	tokens    TokenStore
	sealer    Sealer
	provider  ProviderClient
	secrets   SecretLookup
}

var _ OAuthService = (*Service)(nil)

// New constructs the oauth service. It fails fast on an invalid Config or a nil
// collaborator so a misconfigured module cannot start.
func New(
	cfg Config,
	connectors *connector.Config,
	identity Identity,
	states StateStore,
	tokens TokenStore,
	sealer Sealer,
	provider ProviderClient,
	secrets SecretLookup,
) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if connectors == nil || identity == nil || states == nil || tokens == nil ||
		sealer == nil || provider == nil || secrets == nil {
		return nil, errs.New(errs.KindInternal, "oauth.misconfigured",
			"oauth service is missing a required dependency")
	}

	platforms := make(map[connector.Platform]connector.PlatformConfig, len(connectors.Connectors))
	for _, pc := range connectors.Connectors {
		platforms[pc.Platform] = pc
	}

	return &Service{
		cfg:       cfg,
		platforms: platforms,
		identity:  identity,
		states:    states,
		tokens:    tokens,
		sealer:    sealer,
		provider:  provider,
		secrets:   secrets,
	}, nil
}

// Authorize mints CSRF state and a PKCE verifier, stores them bound to the user
// with a TTL, and returns the provider consent URL.
func (s *Service) Authorize(ctx context.Context, provider string) (model.AuthorizeResponse, error) {
	userID, err := s.authUser(ctx)
	if err != nil {
		return model.AuthorizeResponse{}, err
	}
	meta, pc, err := s.resolveEnabled(provider)
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
		UserID:       userID,
		Platform:     string(meta.platform),
		Provider:     provider,
		CodeVerifier: verifier,
	}
	if err := s.states.Save(ctx, state, data, s.cfg.stateTTL()); err != nil {
		return model.AuthorizeResponse{}, errs.Wrap(err, errs.KindUnavailable, "oauth.state_persist",
			"could not persist authorization state")
	}

	authURL, err := buildAuthorizeURL(meta, clientID, s.redirectURI(provider), pc.Auth.Scopes, state, pkceChallenge(verifier))
	if err != nil {
		return model.AuthorizeResponse{}, errs.Wrap(err, errs.KindInternal, "oauth.internal",
			"could not build authorization url")
	}

	return model.AuthorizeResponse{AuthorizationURL: authURL, State: state}, nil
}

// Callback validates the single-use state, exchanges the code under PKCE, seals
// the issued tokens bound to the user, and persists the connection.
func (s *Service) Callback(ctx context.Context, provider string) (model.ConnectionResponse, error) {
	userID, err := s.authUser(ctx)
	if err != nil {
		return model.ConnectionResponse{}, err
	}
	meta, pc, err := s.resolveEnabled(provider)
	if err != nil {
		return model.ConnectionResponse{}, err
	}

	params, ok := callbackParamsFrom(ctx)
	if !ok {
		return model.ConnectionResponse{}, errs.New(errs.KindInternal, "oauth.internal",
			"callback parameters were not supplied")
	}
	if params.Error != "" {
		// The user declined consent or the provider rejected the request. The
		// provider's error text is not echoed back to avoid reflecting untrusted
		// content.
		return model.ConnectionResponse{}, errs.New(errs.KindInvalid, "oauth.authorization_denied",
			"authorization was not granted")
	}
	if params.Code == "" || params.State == "" {
		return model.ConnectionResponse{}, errs.New(errs.KindInvalid, "oauth.invalid_callback",
			"authorization response is missing required parameters")
	}

	data, ok, err := s.states.Consume(ctx, params.State)
	if err != nil {
		return model.ConnectionResponse{}, errs.Wrap(err, errs.KindUnavailable, "oauth.state_lookup",
			"could not validate authorization state")
	}
	// An unknown/expired/reused state, or one minted for a different user or
	// provider, is a single indistinguishable rejection: revealing which failed
	// would help an attacker probe the store.
	if !ok || data.UserID != userID || data.Provider != provider {
		return model.ConnectionResponse{}, errs.New(errs.KindUnauthorized, "oauth.state_invalid",
			"authorization state is invalid or expired")
	}

	clientID, clientSecret, err := s.credentials(pc)
	if err != nil {
		return model.ConnectionResponse{}, err
	}

	res, err := s.provider.Exchange(ctx, ExchangeRequest{
		Provider:       provider,
		TokenURL:       meta.tokenURL,
		AccountInfoURL: pc.BaseURL + meta.accountPath,
		ClientID:       clientID,
		ClientSecret:   clientSecret,
		RedirectURI:    s.redirectURI(provider),
		Code:           params.Code,
		CodeVerifier:   data.CodeVerifier,
	})
	if err != nil {
		return model.ConnectionResponse{}, errs.Wrap(err, errs.KindUnavailable, "oauth.exchange_failed",
			"could not exchange the authorization code")
	}
	if res.AccessToken == "" || res.ProviderAccountID == "" {
		return model.ConnectionResponse{}, errs.New(errs.KindUnavailable, "oauth.exchange_failed",
			"could not exchange the authorization code")
	}

	tok, err := s.sealToken(userID, meta.platform, res, pc.Auth.Scopes)
	if err != nil {
		return model.ConnectionResponse{}, err
	}
	if err := s.tokens.Upsert(ctx, tok); err != nil {
		return model.ConnectionResponse{}, errs.Wrap(err, errs.KindUnavailable, "oauth.persist_failed",
			"could not persist the connection")
	}

	return model.ConnectionResponse{
		Provider:          provider,
		Platform:          tok.Platform,
		ProviderAccountID: tok.ProviderAccountID,
		Scopes:            tok.Scopes,
		ConnectedAt:       time.Now().UTC(),
		ExpiresAt:         tok.AccessExpiresAt,
	}, nil
}

// Connections lists the caller's connections as client-safe metadata.
func (s *Service) Connections(ctx context.Context) ([]model.ConnectionResponse, error) {
	userID, err := s.authUser(ctx)
	if err != nil {
		return nil, err
	}

	conns, err := s.tokens.ListByUser(ctx, userID)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "oauth.list_failed",
			"could not list connections")
	}

	out := make([]model.ConnectionResponse, 0, len(conns))
	for _, c := range conns {
		out = append(out, model.ConnectionResponse{
			Provider:          providerByPlatform[connector.Platform(c.Platform)],
			Platform:          c.Platform,
			ProviderAccountID: c.ProviderAccountID,
			Scopes:            c.Scopes,
			ConnectedAt:       c.ConnectedAt,
			ExpiresAt:         c.AccessExpiresAt,
		})
	}
	return out, nil
}

// Disconnect removes the caller's connection for a provider. It resolves the
// provider from the registry only (not connector config), so a provider that
// was disabled after being connected can still be disconnected.
func (s *Service) Disconnect(ctx context.Context, provider string) error {
	userID, err := s.authUser(ctx)
	if err != nil {
		return err
	}
	meta, ok := providers[provider]
	if !ok {
		return errUnknownProvider(provider)
	}

	deleted, err := s.tokens.DeleteByUserPlatform(ctx, userID, string(meta.platform))
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "oauth.delete_failed",
			"could not disconnect the provider")
	}
	if deleted == 0 {
		return errs.New(errs.KindNotFound, "oauth.not_connected",
			"no connection exists for this provider")
	}
	return nil
}

// authUser resolves the caller, normalizing any failure to a single
// unauthorized error so the underlying cause is never surfaced.
func (s *Service) authUser(ctx context.Context) (uuid.UUID, error) {
	userID, err := s.identity.UserID(ctx)
	if err != nil {
		return uuid.Nil, errs.Wrap(err, errs.KindUnauthorized, "oauth.unauthenticated",
			"authentication is required")
	}
	if userID == uuid.Nil {
		return uuid.Nil, errs.New(errs.KindUnauthorized, "oauth.unauthenticated",
			"authentication is required")
	}
	return userID, nil
}

// resolveEnabled maps a provider name to its metadata and enabled connector
// configuration, which the authorization and callback flows both require.
func (s *Service) resolveEnabled(provider string) (providerMeta, connector.PlatformConfig, error) {
	meta, ok := providers[provider]
	if !ok {
		return providerMeta{}, connector.PlatformConfig{}, errUnknownProvider(provider)
	}
	pc, ok := s.platforms[meta.platform]
	if !ok || !pc.Enabled {
		return providerMeta{}, connector.PlatformConfig{}, errs.New(errs.KindNotFound,
			"oauth.provider_unavailable", "this provider is not available")
	}
	return meta, pc, nil
}

// credentials resolves the OAuth client id and secret from the environment
// variables the connector config names. A missing credential is a server
// misconfiguration, reported without naming the variable.
func (s *Service) credentials(pc connector.PlatformConfig) (clientID, clientSecret string, err error) {
	clientID = s.secrets(pc.Auth.ClientIDEnv)
	clientSecret = s.secrets(pc.Auth.ClientSecretEnv)
	if clientID == "" || clientSecret == "" {
		return "", "", errs.New(errs.KindInternal, "oauth.misconfigured",
			"this provider is not configured")
	}
	return clientID, clientSecret, nil
}

func (s *Service) redirectURI(provider string) string {
	return s.cfg.RedirectBaseURL + "/oauth/" + provider + "/callback"
}

// LiveConnection is a decrypted, ready-to-use platform connection: the access
// token is in the clear, in memory, for the caller to hand to a connector. It is
// never persisted and never logged.
type LiveConnection struct {
	Platform          string
	ProviderAccountID string
	Token             connector.OAuthToken
}

// LiveConnections returns every platform the user has connected, with the access
// token (and refresh token, when present) decrypted.
//
// Decryption happens here because only this module holds the cipher and knows the
// owner-binding AAD. A token that fails to open — a foreign master key, a
// tampered row, a row copied from another user — is skipped, not surfaced, so one
// bad row cannot fail an entire audit and no decrypt oracle is exposed.
func (s *Service) LiveConnections(ctx context.Context, userID uuid.UUID) ([]LiveConnection, error) {
	sealed, err := s.tokens.ListSealed(ctx, userID)
	if err != nil {
		return nil, err
	}

	aad := []byte("oauth_token:" + userID.String())
	out := make([]LiveConnection, 0, len(sealed))

	for _, t := range sealed {
		access, err := s.sealer.Open(crypto.Sealed{Ciphertext: t.AccessTokenEnc, WrappedDEK: t.DEKWrapped}, aad)
		if err != nil {
			// A row that will not open is unusable, not fatal. Skip it.
			continue
		}

		var refresh string
		if len(t.RefreshTokenEnc) > 0 {
			if refreshSealed, derr := model.DecodeSealed(t.RefreshTokenEnc); derr == nil {
				if plain, oerr := s.sealer.Open(refreshSealed, aad); oerr == nil {
					refresh = string(plain)
				}
			}
		}

		var expiry time.Time
		if t.AccessExpiresAt != nil {
			expiry = *t.AccessExpiresAt
		}

		out = append(out, LiveConnection{
			Platform:          t.Platform,
			ProviderAccountID: t.ProviderAccountID,
			Token: connector.OAuthToken{
				AccessToken:  string(access),
				RefreshToken: refresh,
				Expiry:       expiry,
				Scopes:       t.Scopes,
			},
		})
	}

	return out, nil
}

// sealToken envelope-encrypts the issued tokens with an AAD that binds them to
// their owner, so a row copied into another user's record fails to open.
func (s *Service) sealToken(userID uuid.UUID, platform connector.Platform, res ExchangeResult, fallbackScopes []string) (model.EncryptedToken, error) {
	aad := []byte("oauth_token:" + userID.String())

	accessSealed, err := s.sealer.Seal([]byte(res.AccessToken), aad)
	if err != nil {
		return model.EncryptedToken{}, errs.Wrap(err, errs.KindInternal, "oauth.seal_failed",
			"could not secure the access token")
	}

	var refreshEnc []byte
	if res.RefreshToken != "" {
		refreshSealed, err := s.sealer.Seal([]byte(res.RefreshToken), aad)
		if err != nil {
			return model.EncryptedToken{}, errs.Wrap(err, errs.KindInternal, "oauth.seal_failed",
				"could not secure the refresh token")
		}
		refreshEnc, err = model.EncodeSealed(refreshSealed)
		if err != nil {
			return model.EncryptedToken{}, errs.Wrap(err, errs.KindInternal, "oauth.seal_failed",
				"could not secure the refresh token")
		}
	}

	scopes := res.Scopes
	if len(scopes) == 0 {
		scopes = fallbackScopes
	}

	var expiresAt *time.Time
	if !res.Expiry.IsZero() {
		expiry := res.Expiry.UTC()
		expiresAt = &expiry
	}

	return model.EncryptedToken{
		UserID:            userID,
		Platform:          string(platform),
		ProviderAccountID: res.ProviderAccountID,
		AccessTokenEnc:    accessSealed.Ciphertext,
		RefreshTokenEnc:   refreshEnc,
		DEKWrapped:        accessSealed.WrappedDEK,
		Scopes:            scopes,
		AccessExpiresAt:   expiresAt,
	}, nil
}

func errUnknownProvider(provider string) error {
	return errs.New(errs.KindNotFound, "oauth.unknown_provider",
		"unknown provider "+provider)
}
