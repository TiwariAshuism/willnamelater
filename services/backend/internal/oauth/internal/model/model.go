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
//
// A signup flow (Signup = true) has no user yet: it is begun by an anonymous
// visitor, so UserID is the zero value and the captured Email is what the
// callback provisions the account from. Email is the ONLY field carried across
// the redirect for a signup, so it is validated before the state is minted and
// never read back out of a URL.
type StateData struct {
	UserID       uuid.UUID `json:"user_id"`
	Platform     string    `json:"platform"`
	Provider     string    `json:"provider"`
	CodeVerifier string    `json:"code_verifier"`
	// Signup marks a state minted by the anonymous OAuth-as-signup flow. It
	// distinguishes a signup callback from a connect callback so one flow's state
	// can never be replayed against the other endpoint.
	Signup bool `json:"signup,omitempty"`
	// Email is the address captured when an anonymous visitor began signup. It is
	// the identity the callback creates the account from. Empty for the connect
	// flow, which already knows its user.
	Email string `json:"email,omitempty"`
}

// AuthSession is the token bundle a completed signup hands back so the web can
// establish a logged-in session (set cookies) for the freshly created account.
// It mirrors the auth module's login response; the SessionIssuer port produces
// it without this module ever importing auth.
//
// It carries no password, no session secret beyond the tokens the client is
// meant to hold, and never a decrypted OAuth token — the platform credential
// stays sealed in the database.
type AuthSession struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	UserID       uuid.UUID `json:"user_id"`
	Email        string    `json:"email"`
}

// SignupStartRequest is the body of POST /oauth/meta/signup/start: the email an
// anonymous visitor gives before connecting Instagram. The account is created
// from this address plus the Meta identity resolved on the callback.
type SignupStartRequest struct {
	Email string `json:"email" binding:"required"`
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
	// ProviderUserID is the provider's app-scoped id for the person who connected
	// — the handle Meta's deauthorize and data-deletion callbacks use to name whose
	// data must be erased. Empty for providers with no such callback (YouTube), and
	// for connections made before it was captured.
	ProviderUserID  string
	AccessTokenEnc  []byte
	RefreshTokenEnc []byte // nil when the provider issued no refresh token
	DEKWrapped      []byte
	Scopes          []string
	AccessExpiresAt *time.Time
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

// SignedRequest is the body Meta posts to its deauthorize and data-deletion
// callbacks: a single form field carrying `<base64url(hmac)>.<base64url(json)>`.
// It is the ONLY credential on those endpoints — they carry no session — so it is
// verified against the app secret before anything acts on it.
type SignedRequest struct {
	SignedRequest string `json:"signed_request" form:"signed_request"`
}

// DataDeletionResponse is what Meta requires back from a data-deletion callback:
// a URL where the person can check the status of their request, and a
// confirmation code identifying it.
type DataDeletionResponse struct {
	URL              string `json:"url"`
	ConfirmationCode string `json:"confirmation_code"`
}
