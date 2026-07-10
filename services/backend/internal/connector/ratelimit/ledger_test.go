package ratelimit_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/connector/ratelimit"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// youtubeCfg is a quota_units config block matching YouTube Data API v3.
func youtubeCfg(unitsPerDay int) connector.PlatformConfig {
	return connector.PlatformConfig{
		Platform: connector.PlatformYouTube,
		RateLimit: connector.RateLimit{
			Model:       connector.RateLimitQuotaUnits,
			UnitsPerDay: unitsPerDay,
			DefaultCost: 1,
			SearchCost:  100,
		},
	}
}

func TestLedgerDebit(t *testing.T) {
	t.Parallel()

	// A fixed instant; the ledger derives the UTC day from it.
	clockAt := time.Date(2026, 7, 10, 15, 4, 5, 0, time.UTC)

	tests := []struct {
		name       string
		limit      int
		preSpent   int // units already on the day's row before the debit
		units      int
		wantErr    bool
		wantKind   errs.Kind
		wantQuota  bool // expect a *connector.QuotaExhaustedError
		wantRemain int  // Remaining after the debit, when no error
	}{
		{name: "cheap read within budget", limit: 10000, units: 1, wantRemain: 9999},
		{name: "search cost within budget", limit: 10000, units: 100, wantRemain: 9900},
		{name: "exact fill to the limit", limit: 100, preSpent: 99, units: 1, wantRemain: 0},
		{
			name: "one unit over budget", limit: 100, preSpent: 100, units: 1,
			wantErr: true, wantKind: errs.KindQuotaExceeded, wantQuota: true,
		},
		{
			name: "single debit larger than whole budget", limit: 50, units: 51,
			wantErr: true, wantKind: errs.KindQuotaExceeded, wantQuota: true,
		},
		{
			name: "non-positive units rejected", limit: 100, units: 0,
			wantErr: true, wantKind: errs.KindInvalid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := newFakeRepo()
			if tc.preSpent > 0 {
				if _, ok, err := repo.Debit(context.Background(), string(connector.PlatformYouTube),
					time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC), tc.preSpent, tc.limit); err != nil || !ok {
					t.Fatalf("seeding pre-spent failed: ok=%v err=%v", ok, err)
				}
			}

			ledger := ratelimit.NewLedger(repo, newFakeClock(clockAt), []connector.PlatformConfig{youtubeCfg(tc.limit)})

			err := ledger.Debit(context.Background(), connector.PlatformYouTube, tc.units)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Debit() = nil, want error")
				}
				if got := errs.KindOf(err); got != tc.wantKind {
					t.Fatalf("Debit() kind = %v, want %v", got, tc.wantKind)
				}
				if tc.wantQuota {
					var q *connector.QuotaExhaustedError
					if !errors.As(err, &q) {
						t.Fatalf("Debit() error is not *connector.QuotaExhaustedError: %T", err)
					}
					if q.ResetAt.IsZero() || !q.ResetAt.Equal(time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)) {
						t.Fatalf("QuotaExhaustedError.ResetAt = %v, want next UTC midnight", q.ResetAt)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Debit() = %v, want nil", err)
			}
			remaining, err := ledger.Remaining(context.Background(), connector.PlatformYouTube)
			if err != nil {
				t.Fatalf("Remaining() = %v, want nil", err)
			}
			if remaining != tc.wantRemain {
				t.Fatalf("Remaining() = %d, want %d", remaining, tc.wantRemain)
			}
		})
	}
}

func TestLedgerUnconfiguredPlatform(t *testing.T) {
	t.Parallel()

	ledger := ratelimit.NewLedger(newFakeRepo(), newFakeClock(time.Now()),
		[]connector.PlatformConfig{youtubeCfg(10000)})

	if err := ledger.Debit(context.Background(), connector.PlatformInstagram, 1); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("Debit(unconfigured) kind = %v, want KindInvalid", errs.KindOf(err))
	}
	if _, err := ledger.Remaining(context.Background(), connector.PlatformInstagram); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("Remaining(unconfigured) kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

func TestLedgerRepositoryFailureIsWrapped(t *testing.T) {
	t.Parallel()

	secret := "postgres://user:sup3rsecret@db:5432/influaudit"
	repo := newFakeRepo()
	repo.failWith = errors.New(secret)
	ledger := ratelimit.NewLedger(repo, newFakeClock(time.Now()),
		[]connector.PlatformConfig{youtubeCfg(10000)})

	err := ledger.Debit(context.Background(), connector.PlatformYouTube, 1)
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("Debit() kind on repo failure = %v, want KindUnavailable", errs.KindOf(err))
	}
	// The wrapped cause is reachable for logs via errors.Is/As...
	if !errors.Is(err, repo.failWith) {
		t.Fatalf("Debit() error does not wrap the underlying cause")
	}
	// ...but the client-safe Message must not leak it.
	var domain *errs.Error
	if !errors.As(err, &domain) {
		t.Fatalf("Debit() error is not an *errs.Error")
	}
	if domain.Message == secret || domain.Code == secret {
		t.Fatalf("domain error leaks the wrapped cause")
	}
}

// TestLedgerConcurrentDebitsNeverOverrun fans out many goroutines that each try
// to debit a fixed cost against a small budget and asserts the granted debits
// never exceed the budget. This is the core correctness property: the atomic
// conditional debit must serialize concurrent workers so they cannot jointly
// overrun units_limit.
func TestLedgerConcurrentDebitsNeverOverrun(t *testing.T) {
	t.Parallel()

	const (
		limit      = 1000
		cost       = 10
		goroutines = 500 // 500*10 = 5000 units requested against a 1000-unit budget
	)

	repo := newFakeRepo()
	ledger := ratelimit.NewLedger(repo, newFakeClock(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)),
		[]connector.PlatformConfig{youtubeCfg(limit)})

	var granted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize contention
			if err := ledger.Debit(context.Background(), connector.PlatformYouTube, cost); err == nil {
				atomic.AddInt64(&granted, 1)
				return
			} else if errs.KindOf(err) != errs.KindQuotaExceeded {
				t.Errorf("Debit() unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	spent := int(granted) * cost
	if spent > limit {
		t.Fatalf("granted debits spent %d units, exceeding the %d-unit budget", spent, limit)
	}
	if want := limit / cost; int(granted) != want {
		t.Fatalf("granted = %d debits, want exactly %d (budget fully but not over spent)", granted, want)
	}

	remaining, err := ledger.Remaining(context.Background(), connector.PlatformYouTube)
	if err != nil {
		t.Fatalf("Remaining() = %v", err)
	}
	if remaining != limit-spent {
		t.Fatalf("Remaining() = %d, want %d", remaining, limit-spent)
	}
}

// TestLedgerDayBoundaryIsUTC verifies the accounting day rolls at UTC midnight:
// spend on one UTC day does not reduce the next UTC day's remaining budget, even
// though only the injected clock moved.
func TestLedgerDayBoundaryIsUTC(t *testing.T) {
	t.Parallel()

	// 23:30 UTC: still day 10.
	clock := newFakeClock(time.Date(2026, 7, 10, 23, 30, 0, 0, time.UTC))
	repo := newFakeRepo()
	ledger := ratelimit.NewLedger(repo, clock, []connector.PlatformConfig{youtubeCfg(10000)})

	if err := ledger.Debit(context.Background(), connector.PlatformYouTube, 4000); err != nil {
		t.Fatalf("Debit() = %v", err)
	}
	remaining, err := ledger.Remaining(context.Background(), connector.PlatformYouTube)
	if err != nil {
		t.Fatalf("Remaining() = %v", err)
	}
	if remaining != 6000 {
		t.Fatalf("Remaining() before rollover = %d, want 6000", remaining)
	}

	// Advance one hour to 00:30 UTC on day 11: a new accounting day, full budget.
	clock.Advance(time.Hour)
	remaining, err = ledger.Remaining(context.Background(), connector.PlatformYouTube)
	if err != nil {
		t.Fatalf("Remaining() after rollover = %v", err)
	}
	if remaining != 10000 {
		t.Fatalf("Remaining() after UTC rollover = %d, want 10000 (fresh day)", remaining)
	}
}
