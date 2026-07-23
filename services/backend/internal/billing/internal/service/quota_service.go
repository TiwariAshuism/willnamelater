// Package service implements the billing module's business logic: plans,
// subscriptions, Razorpay webhooks, and the audit quota.
//
// The quota API is this module's most important export. CheckAndReserve is the
// single choke point every audit passes through before work is enqueued, and its
// reservation is committed on success AND on partial success -- a partial audit
// delivered value, so it consumes quota -- but released on total failure.
package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Free-tier quota, applied when a user has no live subscription. It is a code
// default rather than a seeded plan row: a free user gets one audit per
// calendar month and no bulk audits.
const (
	freeAuditQuota = 1
	freeBulkQuota  = 0
)

// periodLayout formats a time as the 'YYYY-MM' billing period stored in
// usage_counter.period.
const periodLayout = "2006-01"

// reservationSep separates the fields packed into a ReservationID.
const reservationSep = "|"

// QuotaService is the billing module's most important export: the metered
// quota API other modules depend on to gate work against a user's plan.
//
// Lifecycle: Reserve is called before work is enqueued and immediately consumes
// one unit via an atomic compare-and-set, so an over-quota user never has work
// scheduled and two concurrent callers cannot both take the last unit. Commit
// is called on success or partial success and is a no-op, because the unit was
// already consumed at reserve time. Release is called only on total failure and
// gives the unit back.
type QuotaService interface {
	Reserve(ctx context.Context, userID uuid.UUID, unit model.Unit) (model.ReservationID, error)
	Commit(ctx context.Context, id model.ReservationID) error
	Release(ctx context.Context, id model.ReservationID) error
}

type quotaService struct {
	repo repository.QuotaRepository
	now  func() time.Time
}

var _ QuotaService = (*quotaService)(nil)

// NewQuotaService builds the quota service over repo. A nil now defaults to
// time.Now.
func NewQuotaService(repo repository.QuotaRepository, now func() time.Time) QuotaService {
	if now == nil {
		now = time.Now
	}
	return &quotaService{repo: repo, now: now}
}

// Reserve consumes one unit for the user in the current calendar month if the
// plan quota allows it, returning a token the caller later commits or releases.
// It returns KindQuotaExceeded when the user is already at their limit.
func (s *quotaService) Reserve(ctx context.Context, userID uuid.UUID, unit model.Unit) (model.ReservationID, error) {
	if !unit.Valid() {
		return "", errs.New(errs.KindInvalid, "billing.unknown_unit", "unknown quota unit")
	}

	limit, found, err := s.repo.LivePlanQuota(ctx, userID, unit)
	if err != nil {
		return "", err
	}
	if !found {
		limit = freeQuotaFor(unit)
	}

	period := s.now().UTC().Format(periodLayout)

	granted, err := s.repo.ReserveUnit(ctx, userID, period, unit, limit)
	if err != nil {
		return "", err
	}
	if !granted {
		return "", errs.New(errs.KindQuotaExceeded, "billing.quota_exceeded", "plan quota exceeded for this period")
	}

	return encodeReservation(unit, userID, period), nil
}

// Commit finalizes a reservation. The unit was consumed at reserve time, so a
// commit only validates the token: the consumption already stands, which is
// exactly the desired outcome for a success or partial success.
func (s *quotaService) Commit(_ context.Context, id model.ReservationID) error {
	_, err := parseReservation(id)
	return err
}

// Release returns the unit a reservation consumed. It is the compensating
// action for a total failure, undoing the increment Reserve made.
func (s *quotaService) Release(ctx context.Context, id model.ReservationID) error {
	res, err := parseReservation(id)
	if err != nil {
		return err
	}
	return s.repo.ReleaseUnit(ctx, res.userID, res.period, res.unit)
}

// freeQuotaFor is the free-tier limit for a unit when the user has no live
// subscription.
func freeQuotaFor(unit model.Unit) int {
	if unit == model.UnitBulkAudit {
		return freeBulkQuota
	}
	return freeAuditQuota
}

// reservation is the decoded content of a ReservationID.
type reservation struct {
	unit   model.Unit
	userID uuid.UUID
	period string
}

// encodeReservation packs the fields needed to commit or release into an opaque
// token, avoiding a separate reservation table.
func encodeReservation(unit model.Unit, userID uuid.UUID, period string) model.ReservationID {
	return model.ReservationID(strings.Join([]string{string(unit), userID.String(), period}, reservationSep))
}

// parseReservation decodes and validates a ReservationID, rejecting anything
// malformed with an invalid-input domain error so a bad token never reaches the
// data layer.
func parseReservation(id model.ReservationID) (reservation, error) {
	parts := strings.Split(string(id), reservationSep)
	if len(parts) != 3 {
		return reservation{}, errInvalidReservation()
	}

	unit := model.Unit(parts[0])
	if !unit.Valid() {
		return reservation{}, errInvalidReservation()
	}

	userID, err := uuid.Parse(parts[1])
	if err != nil {
		return reservation{}, errInvalidReservation()
	}

	if _, err := time.Parse(periodLayout, parts[2]); err != nil {
		return reservation{}, errInvalidReservation()
	}

	return reservation{unit: unit, userID: userID, period: parts[2]}, nil
}

// errInvalidReservation builds the domain error for a malformed reservation
// token. A fresh value is returned each call so callers never share state.
func errInvalidReservation() error {
	return errs.New(errs.KindInvalid, "billing.invalid_reservation", "invalid reservation id")
}
