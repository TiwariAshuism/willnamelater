package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeQuotaRepo implements repository.QuotaRepository with the same atomicity
// contract the Postgres implementation must provide: ReserveUnit is a
// compare-and-set, so it is modelled here as a mutex-guarded check-and-increment
// rather than a read followed by a separate write. A fake that read-then-wrote
// would let the concurrency test pass against a repository that is actually
// racy, which would make the test worse than useless.
type fakeQuotaRepo struct {
	mu sync.Mutex

	// used counts consumed units per (user, period, unit).
	used map[string]int

	// planQuota is the limit returned by LivePlanQuota. found reports whether a
	// live subscription exists at all.
	planQuota int
	found     bool

	livePlanErr error
	reserveErr  error
	releaseErr  error

	releaseCalls atomic.Int64
}

func newFakeQuotaRepo() *fakeQuotaRepo {
	return &fakeQuotaRepo{used: map[string]int{}}
}

func key(userID uuid.UUID, period string, unit model.Unit) string {
	return userID.String() + "|" + period + "|" + string(unit)
}

func (f *fakeQuotaRepo) LivePlanQuota(_ context.Context, _ uuid.UUID, _ model.Unit) (int, bool, error) {
	if f.livePlanErr != nil {
		return 0, false, f.livePlanErr
	}
	return f.planQuota, f.found, nil
}

func (f *fakeQuotaRepo) ReserveUnit(_ context.Context, userID uuid.UUID, period string, unit model.Unit, limit int) (bool, error) {
	if f.reserveErr != nil {
		return false, f.reserveErr
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	k := key(userID, period, unit)
	if limit >= 0 && f.used[k]+1 > limit {
		return false, nil
	}
	f.used[k]++
	return true, nil
}

func (f *fakeQuotaRepo) ReleaseUnit(_ context.Context, userID uuid.UUID, period string, unit model.Unit) error {
	f.releaseCalls.Add(1)
	if f.releaseErr != nil {
		return f.releaseErr
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	k := key(userID, period, unit)
	if f.used[k] > 0 {
		f.used[k]--
	}
	return nil
}

func (f *fakeQuotaRepo) usedFor(userID uuid.UUID, period string, unit model.Unit) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used[key(userID, period, unit)]
}

// fixedClock pins the billing period so a test never straddles a month boundary
// and becomes flaky on the 1st.
func fixedClock() func() time.Time {
	t := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

const fixedPeriod = "2026-03"

func TestReserve(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	repoErr := errors.New("datastore unavailable")

	tests := []struct {
		name      string
		unit      model.Unit
		setup     func(*fakeQuotaRepo)
		wantKind  *errs.Kind
		wantUsed  int
		wantToken bool
	}{
		{
			name:      "free tier grants the first audit",
			unit:      model.UnitAudit,
			setup:     func(*fakeQuotaRepo) {}, // no live subscription
			wantUsed:  1,
			wantToken: true,
		},
		{
			name: "free tier refuses the second audit in the same period",
			unit: model.UnitAudit,
			setup: func(f *fakeQuotaRepo) {
				f.used[key(userID, fixedPeriod, model.UnitAudit)] = 1
			},
			wantKind: kindPtr(errs.KindQuotaExceeded),
			wantUsed: 1,
		},
		{
			name:     "free tier has no bulk-audit quota at all",
			unit:     model.UnitBulkAudit,
			setup:    func(*fakeQuotaRepo) {},
			wantKind: kindPtr(errs.KindQuotaExceeded),
			wantUsed: 0,
		},
		{
			name: "live plan quota overrides the free default",
			unit: model.UnitAudit,
			setup: func(f *fakeQuotaRepo) {
				f.found, f.planQuota = true, 3
				f.used[key(userID, fixedPeriod, model.UnitAudit)] = 2
			},
			wantUsed:  3,
			wantToken: true,
		},
		{
			name: "unlimited plan (-1) always grants",
			unit: model.UnitAudit,
			setup: func(f *fakeQuotaRepo) {
				f.found, f.planQuota = true, -1
				f.used[key(userID, fixedPeriod, model.UnitAudit)] = 9999
			},
			wantUsed:  10000,
			wantToken: true,
		},
		{
			name:     "unknown unit is rejected before any datastore call",
			unit:     model.Unit("not_a_unit"),
			setup:    func(*fakeQuotaRepo) {},
			wantKind: kindPtr(errs.KindInvalid),
		},
		{
			name:     "plan lookup failure propagates",
			unit:     model.UnitAudit,
			setup:    func(f *fakeQuotaRepo) { f.livePlanErr = repoErr },
			wantKind: nil, // a foreign error, not a domain error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeQuotaRepo()
			tt.setup(repo)

			svc := NewQuotaService(repo, fixedClock())
			token, err := svc.Reserve(context.Background(), userID, tt.unit)

			switch {
			case tt.wantToken:
				if err != nil {
					t.Fatalf("Reserve: %v", err)
				}
				if token == "" {
					t.Error("expected a reservation token")
				}
			case tt.wantKind != nil:
				if got := errs.KindOf(err); got != *tt.wantKind {
					t.Fatalf("kind = %v, want %v (err=%v)", got, *tt.wantKind, err)
				}
			default:
				if err == nil {
					t.Fatal("expected an error")
				}
			}

			if tt.unit.Valid() {
				if got := repo.usedFor(userID, fixedPeriod, tt.unit); got != tt.wantUsed {
					t.Errorf("used = %d, want %d", got, tt.wantUsed)
				}
			}
		})
	}
}

// The race this guards is the whole reason ReserveUnit must be a compare-and-set:
// on a one-audit free plan, two simultaneous requests must not both be granted.
// Exactly `limit` goroutines may win, no matter how many contend.
func TestReserveIsAtomicUnderConcurrency(t *testing.T) {
	const (
		limit      = 3
		goroutines = 200
	)

	userID := uuid.New()
	repo := newFakeQuotaRepo()
	repo.found, repo.planQuota = true, limit

	svc := NewQuotaService(repo, fixedClock())

	var granted, exceeded atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize contention
			_, err := svc.Reserve(context.Background(), userID, model.UnitAudit)
			switch {
			case err == nil:
				granted.Add(1)
			case errs.KindOf(err) == errs.KindQuotaExceeded:
				exceeded.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := granted.Load(); got != limit {
		t.Errorf("granted = %d, want exactly %d", got, limit)
	}
	if got := exceeded.Load(); got != goroutines-limit {
		t.Errorf("quota-exceeded = %d, want %d", got, goroutines-limit)
	}
	if got := repo.usedFor(userID, fixedPeriod, model.UnitAudit); got != limit {
		t.Errorf("used counter = %d, want %d -- the budget was overrun", got, limit)
	}
}

// Commit must NOT decrement: the unit was consumed at reserve time. A partial
// audit delivered value and therefore consumes quota.
func TestCommitDoesNotReturnTheUnit(t *testing.T) {
	userID := uuid.New()
	repo := newFakeQuotaRepo()
	svc := NewQuotaService(repo, fixedClock())

	token, err := svc.Reserve(context.Background(), userID, model.UnitAudit)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	if err := svc.Commit(context.Background(), token); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if got := repo.usedFor(userID, fixedPeriod, model.UnitAudit); got != 1 {
		t.Errorf("used = %d after Commit, want 1", got)
	}
	if n := repo.releaseCalls.Load(); n != 0 {
		t.Errorf("Commit called ReleaseUnit %d times, want 0", n)
	}
}

// Release is the compensating action for a TOTAL failure: the unit goes back, so
// a failed audit never burns the free tier's single monthly allowance.
func TestReleaseReturnsTheUnit(t *testing.T) {
	userID := uuid.New()
	repo := newFakeQuotaRepo()
	svc := NewQuotaService(repo, fixedClock())

	token, err := svc.Reserve(context.Background(), userID, model.UnitAudit)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := svc.Release(context.Background(), token); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if got := repo.usedFor(userID, fixedPeriod, model.UnitAudit); got != 0 {
		t.Errorf("used = %d after Release, want 0", got)
	}

	// The user may now reserve again in the same period.
	if _, err := svc.Reserve(context.Background(), userID, model.UnitAudit); err != nil {
		t.Errorf("Reserve after Release: %v", err)
	}
}

// A malformed or tampered token must be rejected as invalid input and must never
// reach the data layer, where it could decrement another user's counter.
func TestReservationTokenTampering(t *testing.T) {
	userID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	valid := encodeReservation(model.UnitAudit, userID, fixedPeriod)

	tests := []struct {
		name  string
		token model.ReservationID
	}{
		{"empty", ""},
		{"too few fields", model.ReservationID("audit|" + userID.String())},
		{"too many fields", valid + "|extra"},
		{"unknown unit", model.ReservationID("wire_transfer|" + userID.String() + "|" + fixedPeriod)},
		{"bad uuid", model.ReservationID("audit|not-a-uuid|" + fixedPeriod)},
		{"bad period", model.ReservationID("audit|" + userID.String() + "|March")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeQuotaRepo()
			svc := NewQuotaService(repo, fixedClock())

			if err := svc.Commit(context.Background(), tt.token); errs.KindOf(err) != errs.KindInvalid {
				t.Errorf("Commit kind = %v, want KindInvalid", errs.KindOf(err))
			}
			if err := svc.Release(context.Background(), tt.token); errs.KindOf(err) != errs.KindInvalid {
				t.Errorf("Release kind = %v, want KindInvalid", errs.KindOf(err))
			}
			if n := repo.releaseCalls.Load(); n != 0 {
				t.Errorf("a malformed token reached the data layer (%d calls)", n)
			}
		})
	}
}

// The reservation token is not a secret, but it must round-trip exactly, or a
// Release would decrement the wrong (user, period, unit) cell.
func TestReservationRoundTrip(t *testing.T) {
	userID := uuid.New()
	token := encodeReservation(model.UnitBulkAudit, userID, fixedPeriod)

	got, err := parseReservation(token)
	if err != nil {
		t.Fatalf("parseReservation: %v", err)
	}
	if got.unit != model.UnitBulkAudit || got.userID != userID || got.period != fixedPeriod {
		t.Errorf("round trip = %+v, want unit=%s user=%s period=%s",
			got, model.UnitBulkAudit, userID, fixedPeriod)
	}
	if !strings.Contains(string(token), userID.String()) {
		t.Error("token does not carry the user id")
	}
}

func kindPtr(k errs.Kind) *errs.Kind { return &k }
