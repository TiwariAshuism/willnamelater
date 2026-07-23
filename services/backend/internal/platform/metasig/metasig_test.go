package metasig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

const testSecret = "test-app-secret"

// sign builds a well-formed signed_request over payloadJSON with secret — the
// same construction Meta performs.
func sign(payloadJSON, secret string) string {
	seg := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(seg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sig + "." + seg
}

func TestVerifyAcceptsAGenuineSignedRequest(t *testing.T) {
	t.Parallel()

	sr := sign(`{"user_id":"12345","algorithm":"HMAC-SHA256","issued_at":1700000000}`, testSecret)

	got, err := Verify(sr, testSecret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.UserID != "12345" {
		t.Fatalf("user id = %q, want 12345", got.UserID)
	}
	if got.IssuedAt != 1700000000 {
		t.Fatalf("issued_at = %d", got.IssuedAt)
	}
}

// The security property that matters: a payload signed with the WRONG secret —
// i.e. by anyone who is not Meta — must never be acted on. Deletion callbacks are
// unauthenticated, so this check is the only thing standing between a stranger
// and another user's data.
func TestVerifyRejectsAForgedSignature(t *testing.T) {
	t.Parallel()

	forged := sign(`{"user_id":"12345","algorithm":"HMAC-SHA256"}`, "attacker-secret")

	if _, err := Verify(forged, testSecret); err == nil {
		t.Fatal("a request signed with the wrong secret must be rejected")
	} else if errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("kind = %v, want unauthorized", errs.KindOf(err))
	}
}

// A tampered payload must not verify: the MAC covers the encoded payload segment,
// so swapping in a different user id after signing breaks it. Without this, an
// attacker could replay a genuine request with someone else's user id.
func TestVerifyRejectsATamperedPayload(t *testing.T) {
	t.Parallel()

	genuine := sign(`{"user_id":"12345","algorithm":"HMAC-SHA256"}`, testSecret)
	sig, _, _ := cut(genuine)
	// Same signature, different payload.
	evil := base64.RawURLEncoding.EncodeToString([]byte(`{"user_id":"99999","algorithm":"HMAC-SHA256"}`))

	if _, err := Verify(sig+"."+evil, testSecret); err == nil {
		t.Fatal("a tampered payload must be rejected")
	}
}

func TestVerifyRejectsMalformedAndDowngraded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		signedRequest string
		secret        string
	}{
		{"empty", "", testSecret},
		{"no separator", "just-one-segment", testSecret},
		{"bad base64 signature", "!!!." + base64.RawURLEncoding.EncodeToString([]byte(`{}`)), testSecret},
		{"bad base64 payload", sign(`{}`, testSecret)[:10] + ".!!!", testSecret},
		{"not json", sign(`not-json`, testSecret), testSecret},
		{
			// The algorithm field is attacker-controlled, so a payload naming a
			// different algorithm must be rejected, not honored.
			"downgraded algorithm",
			sign(`{"user_id":"1","algorithm":"none"}`, testSecret),
			testSecret,
		},
		{
			// A verified payload naming nobody gives us nothing to delete.
			"no user id",
			sign(`{"algorithm":"HMAC-SHA256"}`, testSecret),
			testSecret,
		},
		{
			// An unconfigured app must reject every deletion request rather than
			// accept requests from anyone.
			"empty app secret",
			sign(`{"user_id":"1","algorithm":"HMAC-SHA256"}`, ""),
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Verify(tt.signedRequest, tt.secret); err == nil {
				t.Fatal("expected rejection")
			} else if errs.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("kind = %v, want unauthorized", errs.KindOf(err))
			}
		})
	}
}

func cut(s string) (before, after string, found bool) {
	for i := range len(s) {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
