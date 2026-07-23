// Package metasig parses and verifies Meta's `signed_request` — the only thing
// that authenticates Meta's deauthorize and data-deletion callbacks.
//
// Meta sends both callbacks as an unauthenticated POST carrying a single form
// field, `signed_request`, of the form:
//
//	<base64url(HMAC-SHA256(payload, app_secret))>.<base64url(json payload)>
//
// The payload names the app-scoped user whose data must be deleted. Because the
// endpoint holds no session and Meta presents no other credential, the signature
// IS the authentication: a request whose HMAC does not verify against our app
// secret is an anonymous stranger asking us to delete someone's data, and must be
// rejected before anything is read from it.
//
// The package is deliberately pure — no HTTP, no database, no clock — so the
// verification can be exhaustively tested (and so it cannot be tempted to act on
// an unverified payload).
package metasig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// algHMACSHA256 is the only algorithm we accept. Meta's payload names the
// algorithm it used; honoring that name blindly would let a caller downgrade us
// to an algorithm we do not want, so the value is checked, never trusted.
const algHMACSHA256 = "HMAC-SHA256"

// Payload is the verified content of a signed_request. UserID is Meta's
// app-scoped user id — stable for our app, and the only handle the callback gives
// us to find whose data to delete.
type Payload struct {
	UserID    string `json:"user_id"`
	Algorithm string `json:"algorithm"`
	IssuedAt  int64  `json:"issued_at"`
}

// Verify parses signedRequest and returns its payload ONLY if the signature is a
// valid HMAC-SHA256 over the raw payload segment, keyed by appSecret. Every
// failure — malformed shape, bad base64, unknown algorithm, bad signature —
// returns the same unauthorized error kind and no payload, so a caller cannot
// accidentally act on unverified data and an attacker learns nothing about which
// check failed.
//
// An empty appSecret rejects everything: an unconfigured app must not accept
// deletion requests from anyone rather than accept them from everyone.
func Verify(signedRequest, appSecret string) (Payload, error) {
	if appSecret == "" || signedRequest == "" {
		return Payload{}, errUnverified()
	}

	sigPart, payloadPart, ok := strings.Cut(signedRequest, ".")
	if !ok {
		return Payload{}, errUnverified()
	}

	// Meta uses base64url WITHOUT padding for both segments.
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return Payload{}, errUnverified()
	}
	raw, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return Payload{}, errUnverified()
	}

	// The MAC is computed over the ENCODED payload segment exactly as received —
	// not over the decoded JSON — so re-encoding cannot change what was signed.
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(payloadPart))
	if !hmac.Equal(mac.Sum(nil), sig) {
		return Payload{}, errUnverified()
	}

	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, errUnverified()
	}
	// Check the algorithm only AFTER the MAC verifies: the field is attacker-
	// controlled until then. A payload naming any other algorithm is rejected
	// rather than honored.
	if p.Algorithm != algHMACSHA256 {
		return Payload{}, errUnverified()
	}
	if p.UserID == "" {
		// A verified payload naming no user tells us nothing to delete. Treat it as
		// unusable rather than silently succeeding on a no-op.
		return Payload{}, errUnverified()
	}
	return p, nil
}

// errUnverified is the single, uniform failure. It deliberately says nothing
// about which check failed.
func errUnverified() error {
	return errs.New(errs.KindUnauthorized, "meta.signed_request_invalid",
		"the signed request could not be verified")
}
