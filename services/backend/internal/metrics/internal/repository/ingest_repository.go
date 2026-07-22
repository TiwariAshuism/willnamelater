// Package repository is the metrics module's data-access layer: the read
// queries behind the two HTTP routes, the batched transactional writers the
// ingest path uses, and the crypto_salt accessor that backs pseudonymization.
package repository

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// ingestRepository is the write-side implementation. It is stateless: every
// method receives the transaction to run in, so the same value is safe for
// concurrent audits.
type ingestRepository struct{}

// NewIngestRepository builds the write repository.
func NewIngestRepository() IngestRepository {
	return &ingestRepository{}
}

var _ IngestRepository = (*ingestRepository)(nil)

// appendRowPlaceholders writes "($k,$k+1,...)" for the row at rowIdx (0-based)
// with cols columns into b, producing 1-based positional parameters.
func appendRowPlaceholders(b *strings.Builder, rowIdx, cols int) {
	b.WriteByte('(')
	for c := 0; c < cols; c++ {
		if c > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(rowIdx*cols + c + 1))
	}
	b.WriteByte(')')
}

// UpsertPosts writes rows in one multi-row statement and returns the resulting
// database ids keyed by platform_post_id. On conflict it refreshes the row from
// the newest capture rather than ignoring it, so re-auditing an account updates
// its counters instead of silently keeping stale ones.
func (r *ingestRepository) UpsertPosts(ctx context.Context, tx pgx.Tx, rows []model.PostRow) (map[string]uuid.UUID, error) {
	result := make(map[string]uuid.UUID, len(rows))
	if len(rows) == 0 {
		return result, nil
	}

	const cols = 11
	args := make([]any, 0, len(rows)*cols)
	var b strings.Builder
	b.WriteString(`INSERT INTO post (influencer_id, platform, platform_post_id, audit_job_id, ` +
		`posted_at, permalink, caption, like_count, comment_count, share_count, view_count) VALUES `)
	for i, p := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		appendRowPlaceholders(&b, i, cols)
		args = append(args,
			p.InfluencerID, p.Platform, p.PlatformPostID, p.AuditJobID,
			p.PostedAt, p.Permalink, p.Caption, p.LikeCount, p.CommentCount, p.ShareCount, p.ViewCount)
	}
	b.WriteString(` ON CONFLICT (platform, platform_post_id) DO UPDATE SET
		influencer_id = EXCLUDED.influencer_id,
		audit_job_id  = EXCLUDED.audit_job_id,
		posted_at     = EXCLUDED.posted_at,
		permalink     = EXCLUDED.permalink,
		caption       = EXCLUDED.caption,
		like_count    = EXCLUDED.like_count,
		comment_count = EXCLUDED.comment_count,
		share_count   = EXCLUDED.share_count,
		view_count    = EXCLUDED.view_count
		RETURNING id, platform_post_id`)

	qrows, err := tx.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.upsert_posts", "could not persist posts")
	}
	defer qrows.Close()

	for qrows.Next() {
		var (
			id   uuid.UUID
			ppid string
		)
		if err := qrows.Scan(&id, &ppid); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "metrics.scan_upsert_posts", "could not persist posts")
		}
		result[ppid] = id
	}
	if err := qrows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.upsert_posts_rows", "could not persist posts")
	}
	return result, nil
}

// InsertMetricPoints writes the series in one batched statement. The upsert on
// the series key keeps a re-ingested audit idempotent.
func (r *ingestRepository) InsertMetricPoints(ctx context.Context, tx pgx.Tx, rows []model.MetricPointRow) error {
	if len(rows) == 0 {
		return nil
	}

	const cols = 6
	args := make([]any, 0, len(rows)*cols)
	var b strings.Builder
	b.WriteString(`INSERT INTO metric_point ("time", influencer_id, platform, metric, value, audit_job_id) VALUES `)
	for i, p := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		appendRowPlaceholders(&b, i, cols)
		args = append(args, p.Time, p.InfluencerID, p.Platform, p.Metric, p.Value, p.AuditJobID)
	}
	b.WriteString(` ON CONFLICT (influencer_id, platform, metric, "time") DO UPDATE SET
		value        = EXCLUDED.value,
		audit_job_id = EXCLUDED.audit_job_id`)

	if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "metrics.insert_points", "could not persist metric points")
	}
	return nil
}

// InsertComments writes the pseudonymized comment samples in one batched
// statement. The rows carry author_hash only; no natural key exists to dedup on
// because the connector does not surface a per-comment id, so these are
// append-only samples.
func (r *ingestRepository) InsertComments(ctx context.Context, tx pgx.Tx, rows []model.CommentRow) error {
	if len(rows) == 0 {
		return nil
	}

	const cols = 4
	args := make([]any, 0, len(rows)*cols)
	var b strings.Builder
	b.WriteString(`INSERT INTO comment_sample (post_id, author_hash, body, posted_at) VALUES `)
	for i, c := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		appendRowPlaceholders(&b, i, cols)
		args = append(args, c.PostID, c.AuthorHash, c.Body, c.PostedAt)
	}

	if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "metrics.insert_comments", "could not persist comment samples")
	}
	return nil
}

// InsertAudienceDemographics writes the observed demographic buckets in one
// batched statement, upserting on the bucket key so a re-ingested audit refreshes
// the fraction rather than failing on a duplicate. Only observed buckets reach
// here; an absent dimension is simply no rows.
func (r *ingestRepository) InsertAudienceDemographics(ctx context.Context, tx pgx.Tx, rows []model.AudienceDemographicRow) error {
	if len(rows) == 0 {
		return nil
	}

	const cols = 7
	args := make([]any, 0, len(rows)*cols)
	var b strings.Builder
	b.WriteString(`INSERT INTO audience_demographic ` +
		`(influencer_id, audit_job_id, platform, dimension, bucket, fraction, captured_at) VALUES `)
	for i, d := range rows {
		if i > 0 {
			b.WriteByte(',')
		}
		appendRowPlaceholders(&b, i, cols)
		args = append(args, d.InfluencerID, d.AuditJobID, d.Platform, d.Dimension, d.Bucket, d.Fraction, d.CapturedAt)
	}
	b.WriteString(` ON CONFLICT (influencer_id, platform, audit_job_id, dimension, bucket) DO UPDATE SET
		fraction = EXCLUDED.fraction`)

	if _, err := tx.Exec(ctx, b.String(), args...); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "metrics.insert_audience", "could not persist audience demographics")
	}
	return nil
}
