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

	"github.com/google/uuid"
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

// GetByPublicSlug loads a LIVE published report by its public slug. found is
// false, with no error, when no report carries that slug OR when the report has
// been revoked or has passed its expiry — a withdrawn certificate is
// indistinguishable from one that never existed, so revoking consent genuinely
// withdraws access (Meta Platform Terms §3.d).
func (r *PostgresRepository) GetByPublicSlug(ctx context.Context, slug string) (service.PublishedReport, bool, error) {
	const q = `SELECT storage_key, COALESCE(badge_jsonb, '{}'::jsonb), COALESCE(generated_at, created_at)
		FROM report
		WHERE public_slug = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())`

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

// ReportIDOf resolves the stored pdf report row for an audit job. found is false
// when the audit has never been published.
func (r *PostgresRepository) ReportIDOf(ctx context.Context, auditJobID uuid.UUID) (uuid.UUID, bool, error) {
	const q = `SELECT id FROM report WHERE audit_job_id = $1 AND format = 'pdf'`

	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, q, auditJobID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, errs.Wrap(err, errs.KindInternal, "report.read_failed", "could not read published report")
	}
	return id, true, nil
}

// RevokeByAuditJob withdraws a published report and every live share grant on it,
// in one transaction: the public slug stops resolving and no recipient keeps
// access. Revoking an already-revoked report is a no-op, so the call is
// idempotent — a retried revocation must never fail.
func (r *PostgresRepository) RevokeByAuditJob(ctx context.Context, auditJobID uuid.UUID, at time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the published report")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const revokeReport = `UPDATE report SET revoked_at = $2, updated_at = now()
		WHERE audit_job_id = $1 AND revoked_at IS NULL`
	if _, err := tx.Exec(ctx, revokeReport, auditJobID, at); err != nil {
		return errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the published report")
	}

	const revokeGrants = `UPDATE report_share_grant g SET revoked_at = $2, updated_at = now()
		FROM report r
		WHERE g.report_id = r.id AND r.audit_job_id = $1 AND g.revoked_at IS NULL`
	if _, err := tx.Exec(ctx, revokeGrants, auditJobID, at); err != nil {
		return errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the report's share grants")
	}

	if err := tx.Commit(ctx); err != nil {
		return errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the published report")
	}
	return nil
}

// InsertShareGrant records a creator's express direction to share a report with a
// named recipient for a stated purpose — the Meta Platform Terms §3.c evidence
// trail. Each direction is its own row: re-sharing with the same brand records a
// fresh, separately-revocable grant rather than silently extending an old one.
func (r *PostgresRepository) InsertShareGrant(ctx context.Context, g service.ShareGrant) (uuid.UUID, error) {
	const q = `INSERT INTO report_share_grant
			(report_id, granted_by_user_id, recipient, purpose, granted_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	var id uuid.UUID
	err := r.pool.QueryRow(ctx, q,
		g.ReportID, g.GrantedByUserID, g.Recipient, g.Purpose, g.GrantedAt, g.ExpiresAt,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, errs.Wrap(err, errs.KindInternal, "report.grant_failed", "could not record the share grant")
	}
	return id, nil
}

// RevokeGrantsByUser withdraws every live share grant a user made AND revokes
// every published report they granted. This is the total stop behind a user's
// deletion request and behind Meta's deauthorize / data-deletion callbacks: after
// it runs, nothing the user ever shared resolves for anyone. It returns the number
// of grants withdrawn. Idempotent: a second call withdraws nothing and succeeds.
func (r *PostgresRepository) RevokeGrantsByUser(ctx context.Context, userID uuid.UUID, at time.Time) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the user's shares")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Revoke the reports the user granted before the grants themselves, so the
	// join still sees the live grants that identify which reports to pull.
	const revokeReports = `UPDATE report r SET revoked_at = $2, updated_at = now()
		FROM report_share_grant g
		WHERE g.report_id = r.id AND g.granted_by_user_id = $1 AND r.revoked_at IS NULL`
	if _, err := tx.Exec(ctx, revokeReports, userID, at); err != nil {
		return 0, errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the user's reports")
	}

	const revokeGrants = `UPDATE report_share_grant SET revoked_at = $2, updated_at = now()
		WHERE granted_by_user_id = $1 AND revoked_at IS NULL`
	tag, err := tx.Exec(ctx, revokeGrants, userID, at)
	if err != nil {
		return 0, errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the user's shares")
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, errs.Wrap(err, errs.KindInternal, "report.revoke_failed", "could not revoke the user's shares")
	}
	return tag.RowsAffected(), nil
}

// nullTime maps the zero time to nil so an unset expiry is stored as SQL NULL.
func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
