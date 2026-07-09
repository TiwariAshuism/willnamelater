package crypto

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func testKey(t *testing.T, fill byte) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = fill
	}
	return k
}

func newTestCipher(t *testing.T, fill byte) *Cipher {
	t.Helper()
	c, err := NewCipher(testKey(t, fill))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestNewCipherRejectsWrongKeySize(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"too short", 16},
		{"one short", 31},
		{"one long", 33},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewCipher(make([]byte, tt.size)); !errors.Is(err, ErrKeySize) {
				t.Errorf("NewCipher(%d bytes) error = %v, want ErrKeySize", tt.size, err)
			}
		})
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		plaintext []byte
		aad       []byte
	}{
		{"typical oauth token", []byte("ya29.a0AfH6SMBx-real-looking-token"), []byte("oauth_token:user-1")},
		{"empty plaintext", []byte{}, []byte("oauth_token:user-1")},
		{"nil aad", []byte("secret"), nil},
		{"empty aad", []byte("secret"), []byte{}},
		{"binary plaintext", []byte{0x00, 0xff, 0x7f, 0x80}, []byte("aad")},
		{"large plaintext", bytes.Repeat([]byte("x"), 64*1024), []byte("aad")},
	}

	c := newTestCipher(t, 0x01)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := c.Seal(tt.plaintext, tt.aad)
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}

			got, err := c.Open(sealed, tt.aad)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}

			if !bytes.Equal(got, tt.plaintext) {
				t.Errorf("round trip mismatch: got %q, want %q", got, tt.plaintext)
			}
		})
	}
}

// The whole point of the package: what lands in the database must not contain
// the secret.
func TestSealedCiphertextDoesNotContainPlaintext(t *testing.T) {
	c := newTestCipher(t, 0x01)
	secret := []byte("ya29.super-secret-refresh-token")

	sealed, err := c.Seal(secret, []byte("aad"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if bytes.Contains(sealed.Ciphertext, secret) {
		t.Error("plaintext found in Ciphertext")
	}
	if bytes.Contains(sealed.WrappedDEK, secret) {
		t.Error("plaintext found in WrappedDEK")
	}
}

// GCM is broken by nonce reuse. Every Seal must draw a fresh nonce and a fresh
// DEK, so sealing identical input twice must never produce identical output.
func TestSealIsNonDeterministic(t *testing.T) {
	c := newTestCipher(t, 0x01)
	plaintext, aad := []byte("same"), []byte("same")

	first, err := c.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := c.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Error("identical ciphertexts for repeated Seal: nonce or DEK is being reused")
	}
	if bytes.Equal(first.WrappedDEK, second.WrappedDEK) {
		t.Error("identical wrapped DEKs for repeated Seal: DEK is being reused")
	}
}

// AAD binds a ciphertext to its owner. Lifting one user's encrypted token into
// another user's row must fail.
func TestOpenRejectsMismatchedAAD(t *testing.T) {
	c := newTestCipher(t, 0x01)

	sealed, err := c.Seal([]byte("token"), []byte("oauth_token:user-1"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := c.Open(sealed, []byte("oauth_token:user-2")); !errors.Is(err, ErrCiphertext) {
		t.Errorf("Open with foreign aad error = %v, want ErrCiphertext", err)
	}
}

func TestOpenRejectsWrongMasterKey(t *testing.T) {
	sealer := newTestCipher(t, 0x01)
	attacker := newTestCipher(t, 0x02)

	sealed, err := sealer.Seal([]byte("token"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := attacker.Open(sealed, []byte("aad")); !errors.Is(err, ErrCiphertext) {
		t.Errorf("Open with wrong master key error = %v, want ErrCiphertext", err)
	}
}

func TestOpenRejectsTamperedInput(t *testing.T) {
	c := newTestCipher(t, 0x01)

	sealed, err := c.Seal([]byte("token"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(Sealed) Sealed
	}{
		{"flip ciphertext bit", func(s Sealed) Sealed {
			c := bytes.Clone(s.Ciphertext)
			c[len(c)-1] ^= 0x01
			return Sealed{Ciphertext: c, WrappedDEK: s.WrappedDEK}
		}},
		{"flip wrapped dek bit", func(s Sealed) Sealed {
			w := bytes.Clone(s.WrappedDEK)
			w[len(w)-1] ^= 0x01
			return Sealed{Ciphertext: s.Ciphertext, WrappedDEK: w}
		}},
		{"flip nonce bit", func(s Sealed) Sealed {
			c := bytes.Clone(s.Ciphertext)
			c[0] ^= 0x01
			return Sealed{Ciphertext: c, WrappedDEK: s.WrappedDEK}
		}},
		{"truncate ciphertext", func(s Sealed) Sealed {
			return Sealed{Ciphertext: s.Ciphertext[:len(s.Ciphertext)-1], WrappedDEK: s.WrappedDEK}
		}},
		{"empty ciphertext", func(s Sealed) Sealed {
			return Sealed{Ciphertext: nil, WrappedDEK: s.WrappedDEK}
		}},
		{"empty wrapped dek", func(s Sealed) Sealed {
			return Sealed{Ciphertext: s.Ciphertext, WrappedDEK: nil}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := c.Open(tt.mutate(sealed), []byte("aad")); !errors.Is(err, ErrCiphertext) {
				t.Errorf("Open(tampered) error = %v, want ErrCiphertext", err)
			}
		})
	}
}

// A DEK wrapped for one record must not open another record's ciphertext, even
// under the same master key. This is what binding the wrap to aad buys us.
func TestOpenRejectsSwappedDEK(t *testing.T) {
	c := newTestCipher(t, 0x01)

	alice, err := c.Seal([]byte("alice-token"), []byte("oauth_token:alice"))
	if err != nil {
		t.Fatalf("Seal alice: %v", err)
	}
	bob, err := c.Seal([]byte("bob-token"), []byte("oauth_token:bob"))
	if err != nil {
		t.Fatalf("Seal bob: %v", err)
	}

	frankenstein := Sealed{Ciphertext: alice.Ciphertext, WrappedDEK: bob.WrappedDEK}

	if _, err := c.Open(frankenstein, []byte("oauth_token:alice")); !errors.Is(err, ErrCiphertext) {
		t.Errorf("Open(swapped dek) error = %v, want ErrCiphertext", err)
	}
}

func TestRewrapRotatesMasterKeyWithoutTouchingCiphertext(t *testing.T) {
	old := newTestCipher(t, 0x01)
	next := newTestCipher(t, 0x02)

	aad := []byte("oauth_token:user-1")
	secret := []byte("ya29.token")

	sealed, err := old.Seal(secret, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	rotated, err := old.Rewrap(sealed, next, aad)
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}

	// The expensive part, the secret body, was not re-encrypted.
	if !bytes.Equal(rotated.Ciphertext, sealed.Ciphertext) {
		t.Error("Rewrap re-encrypted the ciphertext; it should only rewrap the DEK")
	}
	// The wrapped DEK did change.
	if bytes.Equal(rotated.WrappedDEK, sealed.WrappedDEK) {
		t.Error("Rewrap did not change the wrapped DEK")
	}

	// The new key opens it.
	got, err := next.Open(rotated, aad)
	if err != nil {
		t.Fatalf("Open after Rewrap: %v", err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("after Rewrap got %q, want %q", got, secret)
	}

	// The retired key no longer does.
	if _, err := old.Open(rotated, aad); !errors.Is(err, ErrCiphertext) {
		t.Errorf("retired key opened rotated record: err = %v, want ErrCiphertext", err)
	}
}

func TestRewrapRejectsWrongCurrentKey(t *testing.T) {
	sealer := newTestCipher(t, 0x01)
	stranger := newTestCipher(t, 0x03)
	next := newTestCipher(t, 0x02)

	sealed, err := sealer.Seal([]byte("token"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if _, err := stranger.Rewrap(sealed, next, []byte("aad")); !errors.Is(err, ErrCiphertext) {
		t.Errorf("Rewrap with wrong key error = %v, want ErrCiphertext", err)
	}
}

// A failing entropy source must surface as an error, never as a zero-value or
// predictable key.
func TestSealFailsWhenEntropyUnavailable(t *testing.T) {
	c := newTestCipher(t, 0x01)
	c.rand = errReader{}

	if _, err := c.Seal([]byte("token"), []byte("aad")); err == nil {
		t.Fatal("Seal succeeded with a failing entropy source")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func BenchmarkSeal(b *testing.B) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	c, err := NewCipher(key)
	if err != nil {
		b.Fatal(err)
	}

	plaintext, aad := []byte("ya29.a0AfH6SMBx-token"), []byte("oauth_token:user-1")

	// b.Loop excludes setup from the timed region, so no ResetTimer is needed.
	for b.Loop() {
		if _, err := c.Seal(plaintext, aad); err != nil {
			b.Fatal(err)
		}
	}
}
