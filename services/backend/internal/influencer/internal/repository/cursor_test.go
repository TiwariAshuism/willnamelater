package repository

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// encodeRaw wraps an arbitrary payload the way encodeCursor does, so a test can
// build a token that is valid base64 but malformed inside.
func encodeRaw(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("6f9619ff-8b86-d011-b42d-00cf4fc964ff")
	// Nanosecond precision is retained because the cursor encodes UnixNano.
	original := cursor{createdAt: time.Unix(1_700_000_000, 123_456_789).UTC(), id: id}

	token := encodeCursor(original)
	if token == "" {
		t.Fatal("encodeCursor returned an empty token")
	}

	got, err := decodeCursor(token)
	if err != nil {
		t.Fatalf("decodeCursor returned error: %v", err)
	}
	if !got.createdAt.Equal(original.createdAt) {
		t.Fatalf("createdAt round-trip = %v, want %v", got.createdAt, original.createdAt)
	}
	if got.id != original.id {
		t.Fatalf("id round-trip = %v, want %v", got.id, original.id)
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
	}{
		{name: "not base64", token: "!!!not-base64!!!"},
		{name: "missing separator", token: encodeRaw("1700000000")},
		{name: "non-numeric timestamp", token: encodeRaw("notanumber|6f9619ff-8b86-d011-b42d-00cf4fc964ff")},
		{name: "bad uuid", token: encodeRaw("1700000000|not-a-uuid")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeCursor(tt.token)
			if errs.KindOf(err) != errs.KindInvalid {
				t.Fatalf("decodeCursor(%q) kind = %v, want KindInvalid", tt.token, errs.KindOf(err))
			}
		})
	}
}
