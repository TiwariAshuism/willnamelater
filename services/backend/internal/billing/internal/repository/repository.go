// Package repository is the billing module's data-access layer. It implements
// the apigen-generated BillingRepository plus two hand-written contracts that
// have no HTTP route: PlanReader, needed by the subscribe flow, and
// QuotaRepository, the data layer behind the Quota API.
package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/billing/internal/model"
)

// PlanReader reads full plan rows for the service's write paths, where quota
// limits and the provider plan reference are needed and a client-facing
// PlanResponse would not suffice.
type PlanReader interface {
	// GetPlanByCode returns the active plan with the given code, or a not-found
	// domain error when no active plan has that code.
	GetPlanByCode(ctx context.Context, code string) (model.Plan, error)
}

// QuotaRepository is the data contract behind the Quota API. Quota is consumed
// by other modules rather than exposed as an HTTP route, so it is hand-written
// rather than apigen-generated.
type QuotaRepository interface {
	// LivePlanQuota returns the per-unit quota of the user's live subscription
	// plan. found is false when the user has no live subscription, in which case
	// the service applies the free-tier default. A quota of -1 means unlimited.
	LivePlanQuota(ctx context.Context, userID uuid.UUID, unit model.Unit) (quota int, found bool, err error)

	// ReserveUnit atomically increments the used counter for (userID, period,
	// unit) if and only if it stays within limit, reporting whether the
	// reservation was granted. limit == -1 means unlimited. Implementations MUST
	// perform this as a single compare-and-set statement, never read-then-write,
	// so concurrent callers cannot both succeed against the last unit.
	ReserveUnit(ctx context.Context, userID uuid.UUID, period string, unit model.Unit, limit int) (granted bool, err error)

	// ReleaseUnit atomically decrements the used counter for (userID, period,
	// unit) without dropping below zero. It is the compensating action for a
	// reservation whose work totally failed.
	ReleaseUnit(ctx context.Context, userID uuid.UUID, period string, unit model.Unit) error
}
