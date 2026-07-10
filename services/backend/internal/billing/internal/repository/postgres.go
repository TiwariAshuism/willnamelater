package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/appctx"
	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// liveStatuses are the subscription statuses that count as a currently-billed
// subscription. It matches the partial unique index subscription_one_live_per_user.
const liveStatuses = "('trialing', 'active', 'past_due')"

// Repository is the PostgreSQL-backed billing data layer. It satisfies the
// generated BillingRepository and the hand-written PlanReader and
// QuotaRepository contracts.
type Repository struct {
	pool *db.Pool
}

var (
	_ BillingRepository = (*Repository)(nil)
	_ PlanReader        = (*Repository)(nil)
	_ QuotaRepository   = (*Repository)(nil)
)

// New builds a Repository over pool.
func New(pool *db.Pool) *Repository {
	return &Repository{pool: pool}
}

// ListPlans returns the active plans ordered by price.
func (r *Repository) ListPlans(ctx context.Context) ([]model.PlanResponse, error) {
	const query = `
		SELECT id, code, name, price_micros, currency, audit_quota, bulk_quota, bulk_enabled
		FROM plan
		WHERE active = true
		ORDER BY price_micros, code`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "billing.plans_query", "could not read plans")
	}
	defer rows.Close()

	plans := make([]model.PlanResponse, 0)
	for rows.Next() {
		var p model.PlanResponse
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.PriceMicros, &p.Currency, &p.AuditQuota, &p.BulkQuota, &p.BulkEnabled); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "billing.plans_scan", "could not read plans")
		}
		plans = append(plans, p)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "billing.plans_query", "could not read plans")
	}
	return plans, nil
}

// GetSubscription returns the caller's live subscription. The caller identity
// travels on the context because the generated signature has no user
// parameter.
func (r *Repository) GetSubscription(ctx context.Context) (model.SubscriptionResponse, error) {
	userID, err := appctx.UserID(ctx)
	if err != nil {
		return model.SubscriptionResponse{}, err
	}

	const query = `
		SELECT s.id, s.plan_id, p.code, s.status,
		       s.current_period_start, s.current_period_end, s.cancel_at_period_end
		FROM subscription s
		JOIN plan p ON p.id = s.plan_id
		WHERE s.user_id = $1 AND s.status IN ` + liveStatuses + `
		ORDER BY s.current_period_end DESC
		LIMIT 1`

	var out model.SubscriptionResponse
	err = r.pool.QueryRow(ctx, query, userID).Scan(
		&out.ID, &out.PlanID, &out.PlanCode, &out.Status,
		&out.CurrentPeriodStart, &out.CurrentPeriodEnd, &out.CancelAtPeriodEnd,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SubscriptionResponse{}, errs.New(errs.KindNotFound, "billing.no_subscription", "no active subscription")
	}
	if err != nil {
		return model.SubscriptionResponse{}, errs.Wrap(err, errs.KindUnavailable, "billing.subscription_query", "could not read subscription")
	}
	return out, nil
}

// Subscribe persists a new subscription for the caller. Any existing live
// subscription is canceled first so the subscription_one_live_per_user
// invariant holds; both statements run in one transaction so a failure leaves
// no orphaned state. The req fields other than PlanCode are populated by the
// service from the resolved plan and the provider response.
func (r *Repository) Subscribe(ctx context.Context, req model.SubscribeRequest) (model.SubscriptionResponse, error) {
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return model.SubscriptionResponse{}, errs.Wrap(err, errs.KindInternal, "billing.subscribe_user", "invalid user reference")
	}
	planID, err := uuid.Parse(req.PlanID)
	if err != nil {
		return model.SubscriptionResponse{}, errs.Wrap(err, errs.KindInternal, "billing.subscribe_plan", "invalid plan reference")
	}

	out := model.SubscriptionResponse{
		PlanID:             req.PlanID,
		PlanCode:           req.PlanCode,
		Status:             req.Status,
		CurrentPeriodStart: req.CurrentPeriodStart,
		CurrentPeriodEnd:   req.CurrentPeriodEnd,
	}

	err = db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		const cancelExisting = `
			UPDATE subscription
			SET status = 'canceled', cancel_at_period_end = true
			WHERE user_id = $1 AND status IN ` + liveStatuses

		if _, err := tx.Exec(ctx, cancelExisting, userID); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "billing.subscribe_cancel", "could not update subscription")
		}

		const insert = `
			INSERT INTO subscription (
				user_id, plan_id, status,
				current_period_start, current_period_end,
				external_customer_id, external_subscription_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id`

		var extCustomer, extSub *string
		if req.ExternalCustomerID != "" {
			extCustomer = &req.ExternalCustomerID
		}
		if req.ExternalSubscriptionID != "" {
			extSub = &req.ExternalSubscriptionID
		}

		if err := tx.QueryRow(ctx, insert,
			userID, planID, req.Status,
			req.CurrentPeriodStart, req.CurrentPeriodEnd,
			extCustomer, extSub,
		).Scan(&out.ID); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "billing.subscribe_insert", "could not create subscription")
		}
		return nil
	})
	if err != nil {
		return model.SubscriptionResponse{}, err
	}
	return out, nil
}

// Webhook applies a verified subscription event: it moves the addressed
// subscription to the status the event implies. The service fills EventStatus
// and EventSubscriptionID after the provider has verified and parsed the
// payload. A webhook for an unknown subscription updates no rows and is
// acknowledged without error, which keeps webhook delivery idempotent.
func (r *Repository) Webhook(ctx context.Context, req model.WebhookRequest) error {
	const update = `
		UPDATE subscription
		SET status = $1,
		    cancel_at_period_end = ($1 = 'canceled')
		WHERE external_subscription_id = $2`

	if _, err := r.pool.Exec(ctx, update, req.EventStatus, req.EventSubscriptionID); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "billing.webhook_apply", "could not apply webhook")
	}
	return nil
}

// GetPlanByCode returns the active plan with the given code.
func (r *Repository) GetPlanByCode(ctx context.Context, code string) (model.Plan, error) {
	const query = `
		SELECT id, code, name, price_micros, currency, audit_quota, bulk_quota, bulk_enabled, active
		FROM plan
		WHERE code = $1 AND active = true`

	var p model.Plan
	err := r.pool.QueryRow(ctx, query, code).Scan(
		&p.ID, &p.Code, &p.Name, &p.PriceMicros, &p.Currency,
		&p.AuditQuota, &p.BulkQuota, &p.BulkEnabled, &p.Active,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Plan{}, errs.New(errs.KindNotFound, "billing.plan_not_found", "plan not found")
	}
	if err != nil {
		return model.Plan{}, errs.Wrap(err, errs.KindUnavailable, "billing.plan_query", "could not read plan")
	}
	return p, nil
}

// LivePlanQuota returns the per-unit quota of the user's live subscription plan.
func (r *Repository) LivePlanQuota(ctx context.Context, userID uuid.UUID, unit model.Unit) (int, bool, error) {
	column, err := planQuotaColumn(unit)
	if err != nil {
		return 0, false, err
	}

	// #nosec G201 -- column is one of a fixed set chosen by planQuotaColumn, not
	// caller input, so no untrusted value reaches the query.
	query := fmt.Sprintf(`
		SELECT p.%s
		FROM subscription s
		JOIN plan p ON p.id = s.plan_id
		WHERE s.user_id = $1 AND s.status IN %s
		ORDER BY s.current_period_end DESC
		LIMIT 1`, column, liveStatuses)

	var quota int
	err = r.pool.QueryRow(ctx, query, userID).Scan(&quota)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errs.Wrap(err, errs.KindUnavailable, "billing.quota_lookup", "could not read plan quota")
	}
	return quota, true, nil
}

// ReserveUnit performs the compare-and-set that makes concurrent reservations
// safe.
//
// The race: two requests for the same user's last remaining unit read the
// counter, both see room, and both write an increment — a lost update that lets
// both proceed. A read-then-write in application code cannot prevent it without
// its own locking.
//
// The fix is to make the check and the increment one atomic statement. The
// INSERT ... SELECT ... ON CONFLICT DO UPDATE below either inserts the first
// usage row or, on conflict, increments the existing counter, but only WHERE it
// is still under limit. PostgreSQL takes a row lock on the conflicting row, so
// concurrent statements serialize on it: the first commits the increment, the
// second re-evaluates the WHERE against the already-incremented value, matches
// no row, and RETURNS nothing. Exactly one caller sees a returned row and is
// granted. limit == -1 disables the ceiling.
func (r *Repository) ReserveUnit(ctx context.Context, userID uuid.UUID, period string, unit model.Unit, limit int) (bool, error) {
	column, err := usageColumn(unit)
	if err != nil {
		return false, err
	}

	// #nosec G201 -- column is one of a fixed set chosen by usageColumn, not
	// caller input, so no untrusted value reaches the query.
	query := fmt.Sprintf(`
		INSERT INTO usage_counter (user_id, period, %[1]s)
		SELECT $1, $2, 1
		WHERE $3 = -1 OR $3 >= 1
		ON CONFLICT (user_id, period) DO UPDATE
			SET %[1]s = usage_counter.%[1]s + 1
			WHERE $3 = -1 OR usage_counter.%[1]s < $3
		RETURNING %[1]s`, column)

	var used int
	err = r.pool.QueryRow(ctx, query, userID, period, limit).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errs.Wrap(err, errs.KindUnavailable, "billing.reserve", "could not reserve quota")
	}
	return true, nil
}

// ReleaseUnit gives back one reserved unit, never dropping the counter below
// zero.
func (r *Repository) ReleaseUnit(ctx context.Context, userID uuid.UUID, period string, unit model.Unit) error {
	column, err := usageColumn(unit)
	if err != nil {
		return err
	}

	// #nosec G201 -- column is one of a fixed set chosen by usageColumn, not
	// caller input, so no untrusted value reaches the query.
	query := fmt.Sprintf(`
		UPDATE usage_counter
		SET %[1]s = %[1]s - 1
		WHERE user_id = $1 AND period = $2 AND %[1]s > 0`, column)

	if _, err := r.pool.Exec(ctx, query, userID, period); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "billing.release", "could not release quota")
	}
	return nil
}

// usageColumn maps a unit to its usage_counter column. The returned name is a
// trusted constant, safe to interpolate into a query.
func usageColumn(unit model.Unit) (string, error) {
	switch unit {
	case model.UnitAudit:
		return "audits_used", nil
	case model.UnitBulkAudit:
		return "bulk_audits_used", nil
	default:
		return "", errs.New(errs.KindInvalid, "billing.unknown_unit", "unknown quota unit")
	}
}

// planQuotaColumn maps a unit to its plan quota column. The returned name is a
// trusted constant, safe to interpolate into a query.
func planQuotaColumn(unit model.Unit) (string, error) {
	switch unit {
	case model.UnitAudit:
		return "audit_quota", nil
	case model.UnitBulkAudit:
		return "bulk_quota", nil
	default:
		return "", errs.New(errs.KindInvalid, "billing.unknown_unit", "unknown quota unit")
	}
}
