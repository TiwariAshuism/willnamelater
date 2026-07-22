// Package repository is the waitlist module's PostgreSQL data layer for the
// email_capture table (migration 000034).
package repository

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/model"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/service"
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

// Upsert records an email capture, idempotent on (email, source): a repeat
// submission on the same surface conflicts on the unique index and does nothing,
// so the call always succeeds. props is cast text->jsonb; a nil props binds NULL.
func (r *Repository) Upsert(ctx context.Context, c model.Capture) error {
	const q = `INSERT INTO email_capture (email, source, influencer_id, props)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (email, source) DO NOTHING`

	var props any
	if len(c.Props) > 0 {
		props = string(c.Props)
	}

	_, err := r.pool.Exec(ctx, q, c.Email, string(c.Source), c.InfluencerID, props)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "waitlist.upsert_failed", "could not record the email capture")
	}
	return nil
}
