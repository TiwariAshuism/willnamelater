// Package model holds the data shapes of the oauth module: the client-facing
// response DTOs, the persisted token record, and the transient CSRF state that
// binds an in-flight authorization to its user.
//
// Nothing here holds a plaintext credential. Access and refresh tokens exist in
// this package only as sealed ciphertext (see EncryptedToken); the live,
// decrypted token never touches a persisted type.
package model

import (
	"time"

	"github.com/google/uuid"
)

// AuthorizeResponse is returned by GET /oauth/:provider/authorize. It carries
// the provider consent URL the client must redirect the user to, plus the
// single-use CSRF state minted for this attempt (echoed so a SPA can correlate
// the pending flow without reading it out of the URL).
type AuthorizeResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
}

// ConnectionResponse is the client-safe view of a stored connection. It carries
// only non-secret metadata; the sealed access and refresh tokens are never
// exposed through the API.
type ConnectionResponse struct {
	Provider          string     `json:"provider"`
	Platform          string     `json:"platform"`
	ProviderAccountID string     `json:"provider_account_id"`
	Scopes            []string   `json:"scopes"`
	ConnectedAt       time.Time  `json:"connected_at"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

// StateData is the CSRF/PKCE material stored in Redis for the lifetime of one
// authorization attempt. It binds the opaque state value to the user who began
// the flow (so a callback cannot be replayed against another account) and holds
// the PKCE verifier that proves, at the token exchange, that this is the same
// client that requested the code.
type StateData struct {
	UserID       uuid.UUID `json:"user_id"`
	Platform     string    `json:"platform"`
	Provider     string    `json:"provider"`
	CodeVerifier string    `json:"code_verifier"`
}

// EncryptedToken is a token record ready to persist: every secret is already
// sealed. It maps onto the oauth_token table's columns, none of which ever hold
// plaintext.
//
// AccessTokenEnc together with DEKWrapped form the access token's envelope: the
// pair opens directly with crypto.Cipher.Open. RefreshTokenEnc, when present,
// is a self-contained sealed blob (see EncodeSealed) carrying its own wrapped
// DEK, because the crypto envelope API mints one DEK per Seal call and does not
// expose sealing two secrets under a shared DEK.
type EncryptedToken struct {
	UserID            uuid.UUID
	Platform          string
	ProviderAccountID string
	AccessTokenEnc    []byte
	RefreshTokenEnc   []byte // nil when the provider issued no refresh token
	DEKWrapped        []byte
	Scopes            []string
	AccessExpiresAt   *time.Time
}

// Connection is the persisted, non-secret projection of an oauth_token row used
// to answer GET /oauth/connections. It deliberately excludes every ciphertext
// column so a listing query never pulls secrets into memory.
type Connection struct {
	Platform          string
	ProviderAccountID string
	Scopes            []string
	ConnectedAt       time.Time
	AccessExpiresAt   *time.Time
}
