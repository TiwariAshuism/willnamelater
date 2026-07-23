package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
)

// fakeSaltStore models crypto_salt in memory with ON CONFLICT DO NOTHING
// semantics: the first successful Insert wins and later inserts are no-ops.
type fakeSaltStore struct {
	sealed  crypto.Sealed
	has     bool
	loads   int
	inserts int

	loadErr   error
	insertErr error
}

func (s *fakeSaltStore) Load(_ context.Context, _ string) (crypto.Sealed, bool, error) {
	s.loads++
	if s.loadErr != nil {
		return crypto.Sealed{}, false, s.loadErr
	}
	return s.sealed, s.has, nil
}

func (s *fakeSaltStore) Insert(_ context.Context, _ string, sealed crypto.Sealed) error {
	s.inserts++
	if s.insertErr != nil {
		return s.insertErr
	}
	if !s.has { // ON CONFLICT DO NOTHING: only the first writer wins.
		s.sealed = sealed
		s.has = true
	}
	return nil
}

func TestAuthorHashStableAndKeyed(t *testing.T) {
	p := NewSaltProvider(&fakeSaltStore{}, testCipher(t))
	ctx := context.Background()

	h1, err := p.AuthorHash(ctx, "author-1")
	if err != nil {
		t.Fatalf("AuthorHash: %v", err)
	}
	h1again, err := p.AuthorHash(ctx, "author-1")
	if err != nil {
		t.Fatalf("AuthorHash: %v", err)
	}
	h2, err := p.AuthorHash(ctx, "author-2")
	if err != nil {
		t.Fatalf("AuthorHash: %v", err)
	}

	if !bytes.Equal(h1, h1again) {
		t.Fatalf("same author hashed differently: %x vs %x", h1, h1again)
	}
	if bytes.Equal(h1, h2) {
		t.Fatalf("different authors collided: %x", h1)
	}
	if len(h1) != 32 {
		t.Fatalf("hash length = %d, want 32", len(h1))
	}
}

func TestDifferentSaltYieldsDifferentHash(t *testing.T) {
	cipher := testCipher(t)
	ctx := context.Background()

	// Two independently-seeded stores hold two different random salts, so the
	// same author hashes to two different values. This is exactly why the salt
	// must be a single stable value: two salts fracture the co-commenter graph.
	pA := NewSaltProvider(&fakeSaltStore{}, cipher)
	pB := NewSaltProvider(&fakeSaltStore{}, cipher)

	hA, err := pA.AuthorHash(ctx, "author-1")
	if err != nil {
		t.Fatalf("AuthorHash A: %v", err)
	}
	hB, err := pB.AuthorHash(ctx, "author-1")
	if err != nil {
		t.Fatalf("AuthorHash B: %v", err)
	}
	if bytes.Equal(hA, hB) {
		t.Fatalf("independently-salted providers produced the same hash: %x", hA)
	}
}

func TestSharedStoreConvergesOnOneSalt(t *testing.T) {
	cipher := testCipher(t)
	ctx := context.Background()

	// A shared store means the first provider seeds the salt and the second
	// reads the winner. Both must hash identically — the convergence the reread
	// after a concurrent-safe insert guarantees.
	store := &fakeSaltStore{}
	pA := NewSaltProvider(store, cipher)
	pB := NewSaltProvider(store, cipher)

	hA, err := pA.AuthorHash(ctx, "author-1")
	if err != nil {
		t.Fatalf("AuthorHash A: %v", err)
	}
	hB, err := pB.AuthorHash(ctx, "author-1")
	if err != nil {
		t.Fatalf("AuthorHash B: %v", err)
	}
	if !bytes.Equal(hA, hB) {
		t.Fatalf("providers over a shared salt disagreed: %x vs %x", hA, hB)
	}
}

func TestSaltSeedsOnceThenCaches(t *testing.T) {
	store := &fakeSaltStore{}
	p := NewSaltProvider(store, testCipher(t))
	ctx := context.Background()

	if _, err := p.AuthorHash(ctx, "a"); err != nil {
		t.Fatalf("AuthorHash: %v", err)
	}
	// First use: one miss + one reread = two loads, and exactly one insert.
	if store.inserts != 1 {
		t.Fatalf("inserts = %d, want 1", store.inserts)
	}
	loadsAfterFirst := store.loads

	for i := 0; i < 5; i++ {
		if _, err := p.AuthorHash(ctx, "a"); err != nil {
			t.Fatalf("AuthorHash: %v", err)
		}
	}
	if store.loads != loadsAfterFirst {
		t.Fatalf("salt re-read from the store after caching: loads went %d -> %d", loadsAfterFirst, store.loads)
	}
	if store.inserts != 1 {
		t.Fatalf("salt re-seeded: inserts = %d, want 1", store.inserts)
	}
}

func TestSaltLoadErrorNotCached(t *testing.T) {
	store := &fakeSaltStore{loadErr: errors.New("down")}
	p := NewSaltProvider(store, testCipher(t))
	ctx := context.Background()

	if _, err := p.AuthorHash(ctx, "a"); err == nil {
		t.Fatal("want error while the store is down")
	}
	// Recover: a transient failure must not be memoized forever.
	store.loadErr = nil
	if _, err := p.AuthorHash(ctx, "a"); err != nil {
		t.Fatalf("AuthorHash after recovery: %v", err)
	}
}

func TestSaltOpensExistingRow(t *testing.T) {
	cipher := testCipher(t)
	// Pre-seed the store with a salt sealed under the module AAD, as a prior
	// boot would have left it.
	sealed, err := cipher.Seal(bytes.Repeat([]byte{0x7}, saltSize), aad())
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	store := &fakeSaltStore{sealed: sealed, has: true}
	p := NewSaltProvider(store, cipher)

	if _, err := p.AuthorHash(context.Background(), "a"); err != nil {
		t.Fatalf("AuthorHash over existing salt: %v", err)
	}
	if store.inserts != 0 {
		t.Fatalf("an existing salt must not be re-seeded: inserts = %d", store.inserts)
	}
}
