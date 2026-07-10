package ratelimit_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/connector/ratelimit"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// metaCfg is a bucketed_calls config block matching the Meta Graph API shape.
func metaCfg(callsPerHour int, window string) connector.PlatformConfig {
	return connector.PlatformConfig{
		Platform: connector.PlatformInstagram,
		RateLimit: connector.RateLimit{
			Model:        connector.RateLimitBucketedCalls,
			CallsPerHour: callsPerHour,
			Window:       window,
		},
	}
}

func TestNewBucketsRejectsBadConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  connector.PlatformConfig
	}{
		{name: "unparseable window", cfg: metaCfg(60, "later")},
		{name: "zero calls", cfg: metaCfg(0, "1h")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ratelimit.NewBuckets(ratelimit.SystemClock, []connector.PlatformConfig{tc.cfg}); errs.KindOf(err) != errs.KindInvalid {
				t.Fatalf("NewBuckets() kind = %v, want KindInvalid", errs.KindOf(err))
			}
		})
	}
}

func TestBucketsAcquire(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC))
	buckets, err := ratelimit.NewBuckets(clock, []connector.PlatformConfig{metaCfg(60, "1h")})
	if err != nil {
		t.Fatalf("NewBuckets() = %v", err)
	}

	// The bucket starts full: 60 calls succeed without advancing the clock.
	for i := 0; i < 60; i++ {
		if err := buckets.Acquire(connector.PlatformInstagram, 1); err != nil {
			t.Fatalf("Acquire() call %d = %v, want nil", i+1, err)
		}
	}

	// The 61st is throttled with a positive retry hint (~one refill period).
	err = buckets.Acquire(connector.PlatformInstagram, 1)
	var rl *connector.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("Acquire() over budget = %v, want *connector.RateLimitError", err)
	}
	if errs.KindOf(err) != errs.KindRateLimited {
		t.Fatalf("Acquire() over budget kind = %v, want KindRateLimited", errs.KindOf(err))
	}
	if rl.RetryAfter <= 0 {
		t.Fatalf("RateLimitError.RetryAfter = %v, want positive", rl.RetryAfter)
	}

	// A rejected Acquire consumes nothing: after one refill period, exactly one
	// token is back and one more call succeeds — no sleeping, only the clock.
	clock.Advance(time.Minute) // 60 calls/hour => one token per minute
	if err := buckets.Acquire(connector.PlatformInstagram, 1); err != nil {
		t.Fatalf("Acquire() after refill = %v, want nil", err)
	}
	// The refilled token is spent again; the next call is throttled once more.
	if err := buckets.Acquire(connector.PlatformInstagram, 1); errs.KindOf(err) != errs.KindRateLimited {
		t.Fatalf("Acquire() after spending refill kind = %v, want KindRateLimited", errs.KindOf(err))
	}
}

func TestBucketsAcquireInvalid(t *testing.T) {
	t.Parallel()

	buckets, err := ratelimit.NewBuckets(newFakeClock(time.Now()), []connector.PlatformConfig{metaCfg(60, "1h")})
	if err != nil {
		t.Fatalf("NewBuckets() = %v", err)
	}

	tests := []struct {
		name     string
		platform connector.Platform
		n        int
	}{
		{name: "unconfigured platform", platform: connector.PlatformYouTube, n: 1},
		{name: "non-positive count", platform: connector.PlatformInstagram, n: 0},
		{name: "exceeds bucket capacity", platform: connector.PlatformInstagram, n: 61},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := buckets.Acquire(tc.platform, tc.n); errs.KindOf(err) != errs.KindInvalid {
				t.Fatalf("Acquire() kind = %v, want KindInvalid", errs.KindOf(err))
			}
		})
	}
}

// TestBucketsConcurrentAcquireNeverExceedsBurst freezes the clock and fans out
// many goroutines against a full bucket, asserting exactly the burst count are
// granted and no more — the in-process bucket must be safe for concurrent use.
func TestBucketsConcurrentAcquireNeverExceedsBurst(t *testing.T) {
	t.Parallel()

	const burst = 100
	buckets, err := ratelimit.NewBuckets(newFakeClock(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)),
		[]connector.PlatformConfig{metaCfg(burst, "1h")})
	if err != nil {
		t.Fatalf("NewBuckets() = %v", err)
	}

	var granted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	const goroutines = 400
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			if err := buckets.Acquire(connector.PlatformInstagram, 1); err == nil {
				atomic.AddInt64(&granted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if granted != burst {
		t.Fatalf("granted = %d calls, want exactly %d (bucket burst)", granted, burst)
	}
}
