// Package repository is the analytics module's PostgreSQL data layer for the
// analytics_event table (migration 000033). The table is append-only: events are
// inserted, never updated, and read back only in aggregate.
package repository

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/analytics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/analytics/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Repository is the pgx-backed service.Repository.
type Repository struct {
	pool *db.Pool
}

var _ service.Repository = (*Repository)(nil)

// New builds a Repository over pool.
func New(pool *db.Pool) *Repository {
	return &Repository{pool: pool}
}

// Insert appends one analytics event. Nil pointer fields are bound as SQL NULL by
// pgx, so an absent attribution is stored as NULL. props is cast text->jsonb; a
// nil props binds as NULL::jsonb.
func (r *Repository) Insert(ctx context.Context, ev model.Event) error {
	const q = `INSERT INTO analytics_event
			(event_type, occurred_at, influencer_id, audit_job_id, public_slug,
			 session_id, is_owner, referrer, user_agent_hash, props)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`

	var props any
	if len(ev.Props) > 0 {
		props = string(ev.Props)
	}

	_, err := r.pool.Exec(ctx, q,
		string(ev.EventType), ev.OccurredAt, ev.InfluencerID, ev.AuditJobID, ev.PublicSlug,
		ev.SessionID, ev.IsOwner, ev.Referrer, ev.UserAgentHash, props,
	)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "analytics.insert_failed", "could not record the analytics event")
	}
	return nil
}

// CountByType returns the total event count per type. It backs the optional
// summary read; the raw rows remain the source of truth.
func (r *Repository) CountByType(ctx context.Context) (map[string]int64, error) {
	const q = `SELECT event_type, count(*) FROM analytics_event GROUP BY event_type`

	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "analytics.summary_query", "could not read the event summary")
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var (
			eventType string
			n         int64
		)
		if err := rows.Scan(&eventType, &n); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "analytics.summary_scan", "could not read the event summary")
		}
		out[eventType] = n
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "analytics.summary_rows", "could not read the event summary")
	}
	return out, nil
}
