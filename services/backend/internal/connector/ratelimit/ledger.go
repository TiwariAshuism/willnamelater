package ratelimit

import (
	"context"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Repository is the persistence port for the quota-units ledger. The Postgres
// implementation performs Debit as a SINGLE conditional UPDATE, so the
// check-and-increment is one atomic step against the committed row; any fake
// used in tests MUST preserve that atomicity (guard and increment under one
// lock) to be a faithful double.
type Repository interface {
	// Debit atomically increments the units-used counter for (platform, day) by
	// units, but only if the resulting total would not exceed limit. It reports
	// the new total and ok=true on success; ok=false with a nil error when the
	// debit was rejected because it would exceed limit; a non-nil error only on a
	// data-access failure. platform is the stable platform key; day must be a UTC
	// midnight.
	Debit(ctx context.Context, platform string, day time.Time, units, limit int) (used int, ok bool, err error)
	// Used returns the units already spent for (platform, day), or 0 when no
	// ledger row exists yet.
	Used(ctx context.Context, platform string, day time.Time) (used int, err error)
}

// Ledger enforces the quota_units model: a per-platform daily unit budget held
// in Repository. It is safe for concurrent use; all shared state lives in the
// Repository, whose Debit is atomic.
type Ledger struct {
	repo   Repository
	clock  Clock
	limits map[connector.Platform]int // units per UTC day, keyed by platform
}

// NewLedger builds a Ledger over repo, reading each platform's daily unit budget
// from the quota_units blocks of cfgs. Blocks with any other Model are ignored;
// a platform absent from the result has no quota_units budget and Debit/Remaining
// on it fail with KindInvalid. cfgs is expected to have passed PlatformConfig
// validation, so units_per_day is positive for every quota_units block.
func NewLedger(repo Repository, clock Clock, cfgs []connector.PlatformConfig) *Ledger {
	limits := make(map[connector.Platform]int, len(cfgs))
	for _, c := range cfgs {
		if c.RateLimit.Model == connector.RateLimitQuotaUnits {
			limits[c.Platform] = c.RateLimit.UnitsPerDay
		}
	}
	return &Ledger{repo: repo, clock: clock, limits: limits}
}

// Debit charges units against platform's budget for the current UTC day. It
// returns a *connector.QuotaExhaustedError (classified KindQuotaExceeded) when
// the charge would overrun the day's budget, so a connector's Fetch can degrade
// to a partial audit and reschedule for the next window.
//
// The debit is delegated to Repository as one conditional UPDATE rather than a
// read-then-write. A read-then-write is wrong under concurrency: two workers
// could both read the same used total, both find room, and both write their
// increment, so their combined spend overruns the limit even though neither saw
// it exceeded — a lost update. The conditional UPDATE evaluates the guard and
// applies the increment atomically against the committed row, which the row lock
// serializes, so the budget can never be jointly overrun.
func (l *Ledger) Debit(ctx context.Context, platform connector.Platform, units int) error {
	if units < 1 {
		return errs.New(errs.KindInvalid, "ratelimit.invalid_units", "debit units must be positive")
	}
	limit, ok := l.limits[platform]
	if !ok {
		return errs.New(errs.KindInvalid, "ratelimit.unconfigured_platform",
			"no quota_units budget configured for platform")
	}
	// A single charge larger than the entire day's budget can never fit today.
	// This is also what keeps the Repository's fresh-row INSERT path correct: the
	// conditional guard only protects the on-conflict UPDATE, so the first debit
	// of a day is only safe because it is already known not to exceed the limit.
	if units > limit {
		return connector.NewQuotaExhaustedError(platform, l.nextReset(), nil)
	}

	_, ok, err := l.repo.Debit(ctx, string(platform), l.day(), units, limit)
	if err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "ratelimit.debit_failed",
			"could not record quota debit")
	}
	if !ok {
		return connector.NewQuotaExhaustedError(platform, l.nextReset(), nil)
	}
	return nil
}

// Remaining reports how many units are still available to platform for the
// current UTC day. It returns 0, never negative, when the budget is spent.
func (l *Ledger) Remaining(ctx context.Context, platform connector.Platform) (int, error) {
	limit, ok := l.limits[platform]
	if !ok {
		return 0, errs.New(errs.KindInvalid, "ratelimit.unconfigured_platform",
			"no quota_units budget configured for platform")
	}
	used, err := l.repo.Used(ctx, string(platform), l.day())
	if err != nil {
		return 0, errs.Wrap(err, errs.KindUnavailable, "ratelimit.remaining_failed",
			"could not read quota ledger")
	}
	if remaining := limit - used; remaining > 0 {
		return remaining, nil
	}
	return 0, nil
}

// day is the current accounting day as an explicit UTC midnight. The boundary is
// UTC, not server-local, so the ledger rolls over at the same instant for every
// worker regardless of the host's timezone and matches YouTube's Pacific-quota
// reset only in that both are a fixed daily boundary — the store's own boundary
// is defined here, in UTC, and nowhere else.
func (l *Ledger) day() time.Time {
	n := l.clock.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// nextReset is the UTC midnight that ends the current accounting day: when the
// budget becomes available again.
func (l *Ledger) nextReset() time.Time {
	return l.day().AddDate(0, 0, 1)
}
