package ratelimit

import (
	"golang.org/x/time/rate"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Buckets enforces the bucketed_calls model with one in-process token bucket per
// platform. The bucket starts full (burst = one window's worth of calls) and
// refills continuously at calls_per_window / window, which approximates a rolling
// window: a caller may burst up to a full window's calls, after which throughput
// is capped at the refill rate. rate.Limiter is safe for concurrent use, so
// Buckets is too.
type Buckets struct {
	clock    Clock
	limiters map[connector.Platform]*rate.Limiter
}

// NewBuckets builds the per-platform buckets from the bucketed_calls blocks of
// cfgs. Blocks with any other Model are ignored. It returns KindInvalid if a
// bucketed_calls block has a non-positive call count or an unparseable window;
// PlatformConfig validation normally rejects those first, so this is defence in
// depth.
func NewBuckets(clock Clock, cfgs []connector.PlatformConfig) (*Buckets, error) {
	limiters := make(map[connector.Platform]*rate.Limiter, len(cfgs))
	for _, c := range cfgs {
		if c.RateLimit.Model != connector.RateLimitBucketedCalls {
			continue
		}
		window, err := c.RateLimit.WindowDuration()
		if err != nil {
			return nil, errs.Wrap(err, errs.KindInvalid, "ratelimit.bad_window",
				"bucketed_calls window is not a valid duration")
		}
		if window <= 0 || c.RateLimit.CallsPerHour < 1 {
			return nil, errs.New(errs.KindInvalid, "ratelimit.bad_bucket",
				"bucketed_calls requires a positive call count and window")
		}
		// calls_per_hour tokens replenish over one window; burst is a full
		// window so an idle connector may spend its whole allowance at once.
		perSecond := rate.Limit(float64(c.RateLimit.CallsPerHour) / window.Seconds())
		limiters[c.Platform] = rate.NewLimiter(perSecond, c.RateLimit.CallsPerHour)
	}
	return &Buckets{clock: clock, limiters: limiters}, nil
}

// Acquire reserves n calls against platform's bucket for an imminent burst of
// requests. It never blocks: when the tokens are available it consumes them and
// returns nil; when they are not it consumes nothing and returns a
// *connector.RateLimitError (classified KindRateLimited) whose RetryAfter is how
// long until n tokens would be available, letting Fetch reschedule instead of
// failing. A request larger than the bucket capacity can never be served and is
// reported as KindInvalid.
func (b *Buckets) Acquire(platform connector.Platform, n int) error {
	lim, ok := b.limiters[platform]
	if !ok {
		return errs.New(errs.KindInvalid, "ratelimit.unconfigured_platform",
			"no bucketed_calls rate limit configured for platform")
	}
	if n < 1 {
		return errs.New(errs.KindInvalid, "ratelimit.invalid_calls", "call count must be positive")
	}

	now := b.clock.Now()
	res := lim.ReserveN(now, n)
	if !res.OK() {
		// n exceeds the bucket's burst; no amount of waiting satisfies it.
		return errs.New(errs.KindInvalid, "ratelimit.exceeds_bucket",
			"requested call count exceeds the bucket capacity")
	}
	if delay := res.DelayFrom(now); delay > 0 {
		// Not enough tokens yet. Return the reservation so we do not silently
		// consume future capacity for a call we are not making now, and report
		// how long the caller should wait.
		res.CancelAt(now)
		return connector.NewRateLimitError(platform, delay, nil)
	}
	return nil
}
