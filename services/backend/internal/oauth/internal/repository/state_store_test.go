package repository

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
)

// This task ships no Redis, so the store's true invariant — that Consume fetches
// and deletes atomically via GETDEL, making a state single-use even under two
// concurrent callbacks — is NOT exercised here. It can only be proven against a
// live server and is deferred to an integration test that does not yet exist.
// What is unit-testable without Redis are the two pure pieces these methods rest
// on: the key namespacing and the StateData JSON round trip. They are covered
// below.

func TestStateKeyIsNamespaced(t *testing.T) {
	t.Parallel()

	key := stateKey("abc123")
	if !strings.HasPrefix(key, stateKeyPrefix) {
		t.Fatalf("key %q is not under the %q namespace", key, stateKeyPrefix)
	}
	if key != stateKeyPrefix+"abc123" {
		t.Fatalf("key = %q, want %q", key, stateKeyPrefix+"abc123")
	}
}

func TestStateKeyDistinctPerState(t *testing.T) {
	t.Parallel()

	if stateKey("one") == stateKey("two") {
		t.Fatal("distinct state values produced the same key")
	}
}

func TestStateDataRoundTrips(t *testing.T) {
	t.Parallel()

	want := model.StateData{
		UserID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Platform:     "youtube",
		Provider:     "google",
		CodeVerifier: "a-high-entropy-pkce-verifier",
	}

	payload, err := encodeStateData(want)
	if err != nil {
		t.Fatalf("encodeStateData: %v", err)
	}

	got, err := decodeStateData(payload)
	if err != nil {
		t.Fatalf("decodeStateData: %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestDecodeStateDataRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	if _, err := decodeStateData([]byte("{not json")); err == nil {
		t.Fatal("malformed payload decoded without error")
	}
}
