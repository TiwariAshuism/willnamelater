package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/getnyx/influaudit/backend/internal/connector"
)

// Public provider names accepted on the :provider path segment. They are the
// user-facing identity provider, distinct from the internal connector platform
// they map to.
const (
	ProviderGoogle = "google" // YouTube, via Google OAuth
	ProviderMeta   = "meta"   // Instagram, via Meta OAuth
)

// providerMeta is the static, non-secret description of one OAuth provider: the
// connector platform it authorizes, its authorization and token endpoints, how
// its scope list is delimited, the account-info path appended to the connector
// base URL to resolve the account id, and any extra authorization-URL
// parameters the provider requires.
//
// Scopes are never listed here: they are read from connector configuration so a
// scope change is a config edit, not a code change.
type providerMeta struct {
	platform    connector.Platform
	authURL     string
	tokenURL    string
	scopeSep    string
	accountPath string
	extraAuth   map[string]string
}

// providers is the closed set of providers this module exposes. Adding one is a
// deliberate change here plus a connector config block.
// #nosec G101 -- the tokenURL fields below are PUBLIC OAuth endpoint URLs
// published by Google and Meta, not credentials. gosec's heuristic matches on
// the substring "token".
var providers = map[string]providerMeta{
	ProviderGoogle: {
		platform:    connector.PlatformYouTube,
		authURL:     "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL:    "https://oauth2.googleapis.com/token",
		scopeSep:    " ",
		accountPath: "/channels?part=id&mine=true",
		// access_type=offline with prompt=consent is what makes Google return a
		// refresh token (and re-return it on re-consent) rather than only an
		// access token.
		extraAuth: map[string]string{
			"access_type":            "offline",
			"prompt":                 "consent",
			"include_granted_scopes": "true",
		},
	},
	ProviderMeta: {
		platform: connector.PlatformInstagram,
		authURL:  "https://www.facebook.com/v21.0/dialog/oauth",
		tokenURL: "https://graph.facebook.com/v21.0/oauth/access_token",
		scopeSep: ",",
		// /me?fields=id returns the Facebook USER id, which is the wrong node for
		// the Instagram Graph API. The audit needs the numeric Instagram Business
		// account id, which hangs off the user's managed Pages. This traversal
		// (granted by pages_show_list + instagram_basic) resolves it; a login with
		// no linked IG business account has no usable id and is rejected honestly.
		accountPath: "/me?fields=id,accounts{instagram_business_account{id,username}}",
	},
}

// providerByPlatform is the reverse index, built once, used to label a stored
// connection (keyed by platform) with its public provider name.
var providerByPlatform = func() map[connector.Platform]string {
	m := make(map[connector.Platform]string, len(providers))
	for name, meta := range providers {
		m[meta.platform] = name
	}
	return m
}()

// buildAuthorizeURL assembles the provider consent URL with PKCE. url.Values
// encodes every value, so scope separators and redirect URIs are escaped
// correctly regardless of provider.
func buildAuthorizeURL(meta providerMeta, clientID, redirectURI string, scopes []string, state, challenge string) (string, error) {
	u, err := url.Parse(meta.authURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize url: %w", err)
	}

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(scopes, meta.scopeSep))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	for k, v := range meta.extraAuth {
		q.Set(k, v)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// randomURLToken returns n bytes of cryptographic randomness as an unpadded
// base64url string, suitable for both the state value and the PKCE verifier.
func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceChallenge derives the S256 code challenge from a verifier:
// base64url(sha256(verifier)), unpadded, per RFC 7636.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
