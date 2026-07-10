// Package password hashes and verifies user passwords with argon2id.
//
// argon2id is the memory-hard, side-channel-resistant password KDF recommended
// by RFC 9106 and OWASP. Hashes are stored in the PHC string format so that the
// parameters used to derive a hash travel with it and can be tuned over time
// without invalidating existing hashes.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id cost parameters. These follow the OWASP Password Storage Cheat
// Sheet's argon2id guidance and are named so the trade-off is reviewable:
//   - memoryKiB dominates the cost of a brute-force rig; 64 MiB per hash prices
//     out large-scale GPU/ASIC attacks while remaining affordable per login.
//   - iterations is the time cost: passes over that memory. Three passes keep a
//     single hash near ~50 ms on server hardware.
//   - parallelism is the number of lanes; two matches a modest per-request core
//     budget without letting one login monopolise the box.
//   - saltLen is 128 bits, the RFC 9106 minimum that makes precomputation and
//     cross-user collisions infeasible.
//   - keyLen is a 256-bit derived key, matching the security level of the rest
//     of the system's symmetric cryptography.
const (
	memoryKiB   = 64 * 1024
	iterations  = 3
	parallelism = 2
	saltLen     = 16
	keyLen      = 32
)

// scheme is the PHC identifier for the algorithm these hashes use.
const scheme = "argon2id"

// ErrInvalidHash is returned when an encoded hash cannot be parsed. It never
// reveals which part was malformed.
var ErrInvalidHash = errors.New("password: invalid encoded hash")

// Hash derives an argon2id hash of plaintext under a fresh random salt and
// returns it in PHC string form, e.g.
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>.
func Hash(plaintext string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: read salt: %w", err)
	}

	key := argon2.IDKey([]byte(plaintext), salt, iterations, memoryKiB, parallelism, keyLen)

	return fmt.Sprintf(
		"$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		scheme, argon2.Version, memoryKiB, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether plaintext matches encoded. The comparison is
// constant-time so a caller cannot learn how many leading bytes were correct
// from timing. A false result with a nil error means "wrong password"; a
// non-nil error means the stored hash could not be parsed.
func Verify(plaintext, encoded string) (bool, error) {
	salt, key, params, err := decode(encoded)
	if err != nil {
		return false, err
	}

	// keyLen is a constant, and decode has already pinned len(key) to it, so no
	// caller-influenced integer conversion reaches argon2 here.
	computed := argon2.IDKey([]byte(plaintext), salt, params.iterations, params.memoryKiB, params.parallelism, keyLen)

	return subtle.ConstantTimeCompare(key, computed) == 1, nil
}

// params holds the cost parameters recovered from an encoded hash so that Verify
// re-derives with the exact settings the stored hash was produced under.
type params struct {
	memoryKiB   uint32
	iterations  uint32
	parallelism uint8
}

// decode parses a PHC-form argon2id hash into its salt, key, and parameters.
func decode(encoded string) (salt, key []byte, p params, err error) {
	parts := strings.Split(encoded, "$")
	// "", scheme, version, params, salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != scheme {
		return nil, nil, params{}, ErrInvalidHash
	}

	var version int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, nil, params{}, ErrInvalidHash
	}

	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memoryKiB, &p.iterations, &p.parallelism); err != nil {
		return nil, nil, params{}, ErrInvalidHash
	}

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, params{}, ErrInvalidHash
	}

	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, params{}, ErrInvalidHash
	}

	// Pin the salt and key to the lengths this scheme emits. A stored hash with
	// any other length was not produced by Hash, so it is corrupt or tampered;
	// accepting it would let an attacker who can write to the users table steer
	// argon2's keyLen parameter.
	if len(salt) != saltLen || len(key) != keyLen {
		return nil, nil, params{}, ErrInvalidHash
	}

	return salt, key, p, nil
}
