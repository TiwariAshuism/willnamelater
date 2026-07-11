// Package repository is the report module's data-access layer for the report
// table (migration 000012 + 000021). The report module is otherwise table-less —
// it assembles the live deliverable from other modules on demand — but a
// *published* report is durable: its rendered PDF lives in object storage and
// its public badge snapshot lives here, keyed by a stable public slug so a
// shared link resolves without re-reading any private data.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/report/internal/service"
)

// PostgresRepository is the pgx-backed service.Repository.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

var _ service.Repository = (*PostgresRepository)(nil)

// New builds a PostgresRepository over pool.
func New(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// UpsertReport persists (or restates) the published PDF report for an audit,
// keyed on (audit_job_id, format). A re-publish overwrites the storage key,
// badge snapshot, size, checksum, and timestamps, but PRESERVES the original
// public slug (COALESCE), so a badge link shared after the first publish keeps
// working across every subsequent re-render.
func (r *PostgresRepository) UpsertReport(ctx context.Context, rec service.ReportRecord) (string, error) {
	badge, err := json.Marshal(rec.Badge)
	if err != nil {
		return "", errs.Wrap(err, errs.KindInternal, "report.badge_encode", "could not encode badge snapshot")
	}

	const q = `INSERT INTO report
			(audit_job_id, format, status, storage_key, public_slug, badge_jsonb, size_bytes, checksum, generated_at, expires_at)
		VALUES ($1, 'pdf', 'ready', $2, $3, $4::jsonb, $5, $6, $7, $8)
		ON CONFLICT (audit_job_id, format) DO UPDATE SET
			status       = EXCLUDED.status,
			storage_key  = EXCLUDED.storage_key,
			public_slug  = COALESCE(report.public_slug, EXCLUDED.public_slug),
			badge_jsonb  = EXCLUDED.badge_jsonb,
			size_bytes   = EXCLUDED.size_bytes,
			checksum     = EXCLUDED.checksum,
			generated_at = EXCLUDED.generated_at,
			expires_at   = EXCLUDED.expires_at
		RETURNING public_slug`

	var slug string
	err = r.pool.QueryRow(ctx, q,
		rec.AuditJobID, rec.StorageKey, rec.PublicSlug, badge,
		rec.SizeBytes, rec.Checksum, rec.GeneratedAt, nullTime(rec.ExpiresAt),
	).Scan(&slug)
	if err != nil {
		return "", errs.Wrap(err, errs.KindInternal, "report.persist_failed", "could not persist published report")
	}
	// The stored slug wins (it may be the pre-existing one preserved by COALESCE),
	// so the caller shares the durable link, not the just-generated candidate.
	return slug, nil
}

// GetByPublicSlug loads a published report by its public slug. found is false,
// with no error, when no report carries that slug.
func (r *PostgresRepository) GetByPublicSlug(ctx context.Context, slug string) (service.PublishedReport, bool, error) {
	const q = `SELECT storage_key, COALESCE(badge_jsonb, '{}'::jsonb), COALESCE(generated_at, created_at)
		FROM report WHERE public_slug = $1`

	var (
		storageKey string
		badgeRaw   []byte
		generated  time.Time
	)
	if err := r.pool.QueryRow(ctx, q, slug).Scan(&storageKey, &badgeRaw, &generated); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.PublishedReport{}, false, nil
		}
		return service.PublishedReport{}, false, errs.Wrap(err, errs.KindInternal, "report.read_failed", "could not read published report")
	}

	var badge service.BadgeSnapshot
	if err := json.Unmarshal(badgeRaw, &badge); err != nil {
		return service.PublishedReport{}, false, errs.Wrap(err, errs.KindInternal, "report.badge_decode", "could not decode badge snapshot")
	}
	return service.PublishedReport{StorageKey: storageKey, Badge: badge, GeneratedAt: generated}, true, nil
}

// nullTime maps the zero time to nil so an unset expiry is stored as SQL NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
