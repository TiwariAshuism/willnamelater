package model

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
)

// ErrSealedEncoding is returned when a stored refresh-token blob is too short or
// malformed to decode. It is deliberately opaque for the same reason
// crypto.ErrCiphertext is: a decode failure must not become an oracle.
var ErrSealedEncoding = errors.New("model: sealed blob is malformed")

// ErrSealedTooLarge is returned when a wrapped DEK cannot be described by the
// 4-byte length prefix. crypto.Cipher wraps a fixed 32-byte DEK, so this is
// unreachable in practice; encoding a truncated length silently would corrupt
// the blob, so it is an error rather than an assumption.
var ErrSealedTooLarge = errors.New("model: wrapped dek exceeds the length prefix")

// EncodeSealed serializes a crypto.Sealed into one self-describing byte slice of
// the form: uint32(len(WrappedDEK)) big-endian || WrappedDEK || Ciphertext.
//
// This exists because the oauth_token schema has a single dek_wrapped column but
// two secret columns, while crypto.Cipher.Seal mints a fresh DEK per call. The
// access token uses the dedicated dek_wrapped column; the refresh token, which
// gets its own DEK, is stored as this self-contained blob so it remains
// recoverable through the public crypto API alone.
func EncodeSealed(s crypto.Sealed) ([]byte, error) {
	if len(s.WrappedDEK) > math.MaxUint32 {
		return nil, ErrSealedTooLarge
	}

	out := make([]byte, 4+len(s.WrappedDEK)+len(s.Ciphertext))
	// #nosec G115 -- the len(s.WrappedDEK) > math.MaxUint32 guard immediately
	// above makes this conversion lossless; gosec cannot see the bound.
	binary.BigEndian.PutUint32(out[:4], uint32(len(s.WrappedDEK)))
	n := copy(out[4:], s.WrappedDEK)
	copy(out[4+n:], s.Ciphertext)
	return out, nil
}

// DecodeSealed reverses EncodeSealed. It is used by consumers that later need
// the refresh token (and by this module's tests) to reconstruct the envelope
// before crypto.Cipher.Open.
func DecodeSealed(blob []byte) (crypto.Sealed, error) {
	if len(blob) < 4 {
		return crypto.Sealed{}, ErrSealedEncoding
	}
	// uint32 -> int is never negative on a 64-bit platform, so only the upper
	// bound needs checking; a length that runs past the blob is malformed.
	wrappedLen := int(binary.BigEndian.Uint32(blob[:4]))
	if 4+wrappedLen > len(blob) {
		return crypto.Sealed{}, ErrSealedEncoding
	}
	wrapped := blob[4 : 4+wrappedLen]
	ciphertext := blob[4+wrappedLen:]
	return crypto.Sealed{
		Ciphertext: append([]byte(nil), ciphertext...),
		WrappedDEK: append([]byte(nil), wrapped...),
	}, nil
}
