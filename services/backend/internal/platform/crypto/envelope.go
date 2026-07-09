// Package crypto provides envelope encryption for secrets at rest, principally
// the OAuth access and refresh tokens held in the oauth module.
//
// Envelope encryption means each secret gets its own single-use data key (DEK).
// The DEK encrypts the plaintext; a long-lived master key encrypts the DEK.
// Only the ciphertext and the wrapped DEK are stored. Rotating the master key
// therefore rewraps DEKs rather than re-encrypting every secret, and a leaked
// DEK compromises exactly one secret.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// KeySize is the AES-256 key length in bytes, for both master keys and DEKs.
const KeySize = 32

var (
	// ErrKeySize is returned when a master key is not exactly KeySize bytes.
	ErrKeySize = errors.New("crypto: master key must be 32 bytes")
	// ErrCiphertext is returned when a ciphertext is malformed or fails
	// authentication. It is deliberately opaque: distinguishing "wrong key"
	// from "tampered data" would be an oracle.
	ErrCiphertext = errors.New("crypto: ciphertext is invalid")
)

// Sealed is the storable result of encrypting one secret. Both fields are
// ciphertext; neither reveals anything about the plaintext without the master
// key. These map directly onto the *_enc and dek_wrapped database columns.
type Sealed struct {
	// Ciphertext is the secret encrypted under a single-use DEK.
	Ciphertext []byte
	// WrappedDEK is that DEK encrypted under the master key.
	WrappedDEK []byte
}

// Cipher seals and opens secrets under a master key.
//
// The zero value is not usable; construct with NewCipher.
type Cipher struct {
	master cipher.AEAD
	// rand is the entropy source, overridable in tests to assert failure
	// handling. Production always uses crypto/rand.Reader.
	rand io.Reader
}

// NewCipher builds a Cipher from a 32-byte master key. The key is expected to
// come from a KMS or the environment, never from source or the database.
func NewCipher(masterKey []byte) (*Cipher, error) {
	if len(masterKey) != KeySize {
		return nil, fmt.Errorf("%w, got %d", ErrKeySize, len(masterKey))
	}

	aead, err := newAEAD(masterKey)
	if err != nil {
		return nil, err
	}

	return &Cipher{master: aead, rand: rand.Reader}, nil
}

// newAEAD builds an AES-256-GCM AEAD over key. GCM is used in its nonce-
// prepended form throughout this package.
func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new aes cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}

	return aead, nil
}

// Seal encrypts plaintext under a freshly generated DEK and wraps that DEK
// under the master key.
//
// aad is additional authenticated data: it is not encrypted, but the ciphertext
// cannot be opened without presenting the identical value. Callers bind a
// secret to its owner by passing something like []byte("oauth_token:<user_id>"),
// which makes a ciphertext copied into another user's row fail to open.
func (c *Cipher) Seal(plaintext, aad []byte) (Sealed, error) {
	dek := make([]byte, KeySize)
	if _, err := io.ReadFull(c.rand, dek); err != nil {
		return Sealed{}, fmt.Errorf("crypto: generate dek: %w", err)
	}
	// The DEK is a secret in memory; scrub it once the ciphertext exists.
	defer zero(dek)

	dekAEAD, err := newAEAD(dek)
	if err != nil {
		return Sealed{}, err
	}

	ciphertext, err := c.encrypt(dekAEAD, plaintext, aad)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: encrypt plaintext: %w", err)
	}

	// The wrapped DEK is bound to the same aad, so a DEK cannot be lifted from
	// one record and paired with another record's ciphertext.
	wrapped, err := c.encrypt(c.master, dek, aad)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: wrap dek: %w", err)
	}

	return Sealed{Ciphertext: ciphertext, WrappedDEK: wrapped}, nil
}

// Open reverses Seal. aad must equal the value passed to Seal.
//
// Every failure returns ErrCiphertext with no detail, so a caller cannot learn
// whether the master key, the DEK, the aad, or the ciphertext was at fault.
func (c *Cipher) Open(s Sealed, aad []byte) ([]byte, error) {
	dek, err := c.decrypt(c.master, s.WrappedDEK, aad)
	if err != nil {
		return nil, ErrCiphertext
	}
	defer zero(dek)

	dekAEAD, err := newAEAD(dek)
	if err != nil {
		return nil, ErrCiphertext
	}

	plaintext, err := c.decrypt(dekAEAD, s.Ciphertext, aad)
	if err != nil {
		return nil, ErrCiphertext
	}

	return plaintext, nil
}

// encrypt returns nonce||ciphertext||tag. A fresh nonce is drawn per call; GCM
// is catastrophically insecure under nonce reuse, and because every DEK is
// single-use, no nonce is ever paired with the same key twice.
func (c *Cipher) encrypt(aead cipher.AEAD, plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(c.rand, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Append into nonce so the nonce prefixes the output in one allocation.
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

// decrypt splits nonce||ciphertext and authenticates it.
func (c *Cipher) decrypt(aead cipher.AEAD, blob, aad []byte) ([]byte, error) {
	if len(blob) < aead.NonceSize() {
		return nil, ErrCiphertext
	}

	nonce, ciphertext := blob[:aead.NonceSize()], blob[aead.NonceSize():]

	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrCiphertext
	}

	return plaintext, nil
}

// Rewrap re-encrypts s.WrappedDEK under next without touching s.Ciphertext.
// This is the master-key rotation path: it is O(number of secrets) in cheap
// 32-byte operations rather than re-encrypting every secret body.
func (c *Cipher) Rewrap(s Sealed, next *Cipher, aad []byte) (Sealed, error) {
	dek, err := c.decrypt(c.master, s.WrappedDEK, aad)
	if err != nil {
		return Sealed{}, ErrCiphertext
	}
	defer zero(dek)

	wrapped, err := next.encrypt(next.master, dek, aad)
	if err != nil {
		return Sealed{}, fmt.Errorf("crypto: rewrap dek: %w", err)
	}

	return Sealed{Ciphertext: s.Ciphertext, WrappedDEK: wrapped}, nil
}

// zero overwrites b. Go's GC may still have copied the memory, so this is a
// mitigation that shortens a key's lifetime in RAM, not a guarantee.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
