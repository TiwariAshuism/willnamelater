package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

const testWebhookSecret = "whsec_test_value_not_a_real_credential"

// sign produces the signature Razorpay would send for body under secret.
func sign(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func newTestRazorpay() *Razorpay {
	return NewRazorpay(RazorpayConfig{WebhookSecret: testWebhookSecret}, nil, nil)
}

// A webhook is an unauthenticated public endpoint. The signature is the ONLY
// thing standing between an attacker and a forged "subscription activated"
// event, so every rejection path below must hold.
func TestHandleWebhookRejectsBadSignature(t *testing.T) {
	body := []byte(`{"event":"subscription.activated","payload":{"subscription":{"entity":{"id":"sub_1","status":"active"}}}}`)
	valid := sign(t, testWebhookSecret, body)

	tests := []struct {
		name      string
		body      []byte
		signature string
	}{
		{"empty signature", body, ""},
		{"not hex", body, "zzzz-not-hex"},
		{"truncated signature", body, valid[:len(valid)-2]},
		{"one flipped hex digit", body, flipLastHexDigit(valid)},
		{"signature from a different secret", body, sign(t, "another_secret", body)},
		{"valid signature over a different body", []byte(`{"event":"subscription.halted"}`), valid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := newTestRazorpay()

			_, err := rp.HandleWebhook(context.Background(), tt.body, tt.signature)
			if err == nil {
				t.Fatal("a webhook with an invalid signature was accepted")
			}
			if got := errs.KindOf(err); got != errs.KindUnauthorized {
				t.Errorf("kind = %v, want KindUnauthorized", got)
			}
		})
	}
}

func TestHandleWebhookAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"event":"subscription.activated","payload":{"subscription":{"entity":{"id":"sub_abc","status":"active"}}}}`)

	rp := newTestRazorpay()
	event, err := rp.HandleWebhook(context.Background(), body, sign(t, testWebhookSecret, body))
	if err != nil {
		t.Fatalf("HandleWebhook: %v", err)
	}

	if event.Type != "subscription.activated" {
		t.Errorf("Type = %q, want subscription.activated", event.Type)
	}
	if event.ExternalSubscriptionID != "sub_abc" {
		t.Errorf("ExternalSubscriptionID = %q, want sub_abc", event.ExternalSubscriptionID)
	}
}

// A correctly-signed but malformed body is a different failure from a forged
// one: it is the sender's bug, not an attack. Conflating them would either hide
// an integration break or cry wolf on an intrusion.
func TestHandleWebhookMalformedBodyIsInvalidNotUnauthorized(t *testing.T) {
	body := []byte(`{"event": not json`)

	rp := newTestRazorpay()
	_, err := rp.HandleWebhook(context.Background(), body, sign(t, testWebhookSecret, body))
	if err == nil {
		t.Fatal("expected an error for a malformed body")
	}
	if got := errs.KindOf(err); got != errs.KindInvalid {
		t.Errorf("kind = %v, want KindInvalid", got)
	}
}

// The webhook secret must never travel to the client inside an error.
func TestHandleWebhookErrorNeverLeaksTheSecret(t *testing.T) {
	rp := newTestRazorpay()

	_, err := rp.HandleWebhook(context.Background(), []byte(`{}`), "deadbeef")
	if err == nil {
		t.Fatal("expected an error")
	}

	var domain *errs.Error
	if !errors.As(err, &domain) {
		t.Fatalf("error %v is not a domain error", err)
	}
	if strings.Contains(domain.Message, testWebhookSecret) || strings.Contains(domain.Code, testWebhookSecret) {
		t.Error("the webhook secret leaked into a client-facing field")
	}
}

// flipLastHexDigit produces a signature of identical length that differs in one
// nibble, which is what a naive prefix comparison would wrongly accept.
func flipLastHexDigit(sig string) string {
	if sig == "" {
		return "0"
	}
	last := sig[len(sig)-1]
	replacement := byte('0')
	if last == '0' {
		replacement = '1'
	}
	return sig[:len(sig)-1] + string(replacement)
}
