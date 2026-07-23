package ratelimit_test

import (
	"context"
	"sync"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector/ratelimit"
)

// fakeClock is a manually advanced Clock. Every method is safe for concurrent
// use so the ledger concurrency test can share one clock across goroutines.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var _ ratelimit.Clock = (*fakeClock)(nil)

// fakeRepo is an in-memory Repository that faithfully models the Postgres
// implementation's single atomic UPDATE: the guard check and the increment
// happen together under one mutex, exactly as the real statement does under the
// row lock. Without that atomicity it would not be a valid double for the
// concurrency contract under test.
type fakeRepo struct {
	mu   sync.Mutex
	used map[string]int
	// failWith, when non-nil, makes every call return it, exercising the
	// data-access failure path.
	failWith error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{used: make(map[string]int)} }

func key(platform string, day time.Time) string {
	return platform + "|" + day.Format("2006-01-02")
}

func (r *fakeRepo) Debit(_ context.Context, platform string, day time.Time, units, limit int) (int, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failWith != nil {
		return 0, false, r.failWith
	}
	k := key(platform, day)
	if r.used[k]+units > limit {
		return 0, false, nil
	}
	r.used[k] += units
	return r.used[k], true, nil
}

func (r *fakeRepo) Used(_ context.Context, platform string, day time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failWith != nil {
		return 0, r.failWith
	}
	return r.used[key(platform, day)], nil
}

var _ ratelimit.Repository = (*fakeRepo)(nil)
