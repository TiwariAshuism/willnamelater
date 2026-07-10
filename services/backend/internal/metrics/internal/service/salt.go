// Package service holds the metrics module's business logic: the read services
// behind the HTTP routes, the transactional snapshot ingest, and the
// SaltProvider that pseudonymizes commenter identities before they are stored.
package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"sync"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

const (
	// SaltNameCommentAuthor is the crypto_salt.name under which the single,
	// application-wide commenter-pseudonymization salt lives. One salt, one row,
	// forever.
	SaltNameCommentAuthor = "comment_author"
	// saltSize is the salt length in bytes. 32 bytes is a full-strength HMAC-
	// SHA256 key.
	saltSize = 32
)

// SaltProvider loads the application-wide comment-author salt once and caches
// it, then derives keyed hashes of commenter identities from it.
//
// THE SALT MUST NEVER BE ROTATED. comment_sample.author_hash is
// HMAC-SHA256(author_id, salt), and the entire co-commenter graph is built by
// joining comments on equal author_hash across posts. Re-keying the salt maps
// the same commenter to a new hash: every historical co-occurrence edge silently
// vanishes, clique and repeat-commenter counts collapse toward zero, and the
// system then reports "no coordination detected" with full confidence while
// actually having destroyed the evidence. There is deliberately no rotation
// method here, and crypto_salt rejects UPDATE/DELETE at the database, so an
// accidental rotation is impossible rather than merely discouraged.
type SaltProvider struct {
	store  repository.SaltStore
	cipher *crypto.Cipher
	// rand is the entropy source for first-boot salt generation, overridable in
	// tests. Production uses crypto/rand.Reader.
	rand io.Reader

	// mu guards the one-time load. The salt is cached only on success, so a
	// transient database error at first boot is retried on the next call rather
	// than being memoized forever.
	mu    sync.Mutex
	value []byte
}

// NewSaltProvider builds a provider over store and cipher. The cipher must be
// non-nil: pseudonymization is mandatory, so a metrics module wired without a
// master key is a boot-time misconfiguration, not a run-time degradation.
func NewSaltProvider(store repository.SaltStore, cipher *crypto.Cipher) *SaltProvider {
	return &SaltProvider{store: store, cipher: cipher, rand: rand.Reader}
}

// AuthorHash returns HMAC-SHA256(authorID, salt). The salt is loaded and cached
// on the first call; subsequent calls never touch the database.
func (p *SaltProvider) AuthorHash(ctx context.Context, authorID string) ([]byte, error) {
	salt, err := p.salt(ctx)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, salt)
	// hmac.Hash.Write never returns an error.
	_, _ = mac.Write([]byte(authorID))
	return mac.Sum(nil), nil
}

// salt returns the cached salt, loading or seeding it on first use.
func (p *SaltProvider) salt(ctx context.Context) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.value != nil {
		return p.value, nil
	}
	value, err := p.loadOrSeed(ctx)
	if err != nil {
		return nil, err
	}
	p.value = value
	return value, nil
}

// aad binds a sealed salt to its logical name, so a salt row copied under a
// different name would fail to open.
func aad() []byte { return []byte("crypto_salt:" + SaltNameCommentAuthor) }

// loadOrSeed returns the plaintext salt, generating and sealing a fresh one on
// first boot. It always re-reads after an insert so that under a concurrent
// first boot every node converges on the single winning row rather than each
// using the salt it locally generated.
func (p *SaltProvider) loadOrSeed(ctx context.Context) ([]byte, error) {
	if sealed, ok, err := p.store.Load(ctx, SaltNameCommentAuthor); err != nil {
		return nil, err
	} else if ok {
		return p.open(sealed)
	}

	raw := make([]byte, saltSize)
	if _, err := io.ReadFull(p.rand, raw); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "metrics.salt_generate", "could not generate the comment-author salt")
	}
	sealed, err := p.cipher.Seal(raw, aad())
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "metrics.salt_seal", "could not seal the comment-author salt")
	}
	if err := p.store.Insert(ctx, SaltNameCommentAuthor, sealed); err != nil {
		return nil, err
	}

	reread, ok, err := p.store.Load(ctx, SaltNameCommentAuthor)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errs.New(errs.KindInternal, "metrics.salt_missing", "the comment-author salt is missing immediately after seeding")
	}
	return p.open(reread)
}

// open decrypts a sealed salt under the module's stable AAD.
func (p *SaltProvider) open(sealed crypto.Sealed) ([]byte, error) {
	plain, err := p.cipher.Open(sealed, aad())
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "metrics.salt_open", "could not decrypt the comment-author salt")
	}
	return plain, nil
}
