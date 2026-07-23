package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Read caps for the profile summary. The strip and cadence are computed over a
// recent window, and the reach_ratio median over a bounded series, so an account
// with a long history cannot turn a result-page render into an unbounded scan.
const (
	summaryPostWindow      = 30
	summaryReachRatioLimit = 200
)

// GetInfluencerProfileSummary gathers the already-captured data for one influencer
// and folds it into the honest result-page summary. It runs several small
// read-only queries over the metrics-owned tables and delegates every nil/false
// decision to the pure aggregation helpers, so absence is disclosed and never a
// fabricated zero.
func (r *metricsRepository) GetInfluencerProfileSummary(ctx context.Context, id string) (model.ProfileSummaryResponse, error) {
	influencerID, err := uuid.Parse(id)
	if err != nil {
		return model.ProfileSummaryResponse{}, errs.Wrap(err, errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")
	}

	var data model.ProfileSummaryData

	if data.Followers, err = r.latestFollowers(ctx, influencerID); err != nil {
		return model.ProfileSummaryResponse{}, err
	}
	if data.ReachRatios, err = r.reachRatios(ctx, influencerID); err != nil {
		return model.ProfileSummaryResponse{}, err
	}
	if data.Audience, err = r.latestAudience(ctx, influencerID); err != nil {
		return model.ProfileSummaryResponse{}, err
	}
	if data.Posts, err = r.recentPostAggregates(ctx, influencerID); err != nil {
		return model.ProfileSummaryResponse{}, err
	}
	if data.CommentSampleCount, err = r.commentSampleCount(ctx, influencerID); err != nil {
		return model.ProfileSummaryResponse{}, err
	}

	return buildProfileSummary(influencerID.String(), data), nil
}

// latestFollowers returns the newest metric_point 'followers' value, rounded to a
// whole count, or nil when the metric was never recorded.
func (r *metricsRepository) latestFollowers(ctx context.Context, influencerID uuid.UUID) (*int64, error) {
	var value float64
	err := r.pool.QueryRow(ctx, `
		SELECT value FROM metric_point
		WHERE influencer_id = $1 AND metric = 'followers'
		ORDER BY "time" DESC
		LIMIT 1`, influencerID).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_followers", "could not read follower count")
	}
	v := roundToInt64(value)
	return &v, nil
}

// reachRatios returns the recorded metric_point 'reach_ratio' values (bounded) for
// the median in the strip.
func (r *metricsRepository) reachRatios(ctx context.Context, influencerID uuid.UUID) ([]float64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT value FROM metric_point
		WHERE influencer_id = $1 AND metric = 'reach_ratio'
		ORDER BY "time" DESC
		LIMIT $2`, influencerID, summaryReachRatioLimit)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_reach_ratio", "could not read reach ratios")
	}
	defer rows.Close()

	var out []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "metrics.summary_reach_ratio_scan", "could not read reach ratios")
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_reach_ratio_rows", "could not read reach ratios")
	}
	return out, nil
}

// latestAudience returns the observed demographic buckets for the influencer's
// most recent audit (the audit with the latest captured_at). It returns no rows
// when demographics were never pulled — absence is the lack of a row.
func (r *metricsRepository) latestAudience(ctx context.Context, influencerID uuid.UUID) ([]model.AudienceBucket, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT dimension, bucket, fraction
		FROM audience_demographic
		WHERE influencer_id = $1
		  AND audit_job_id = (
		    SELECT audit_job_id FROM audience_demographic
		    WHERE influencer_id = $1
		    ORDER BY captured_at DESC
		    LIMIT 1
		  )`, influencerID)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_audience", "could not read audience demographics")
	}
	defer rows.Close()

	var out []model.AudienceBucket
	for rows.Next() {
		var b model.AudienceBucket
		if err := rows.Scan(&b.Dimension, &b.Bucket, &b.Fraction); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "metrics.summary_audience_scan", "could not read audience demographics")
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_audience_rows", "could not read audience demographics")
	}
	return out, nil
}

// recentPostAggregates returns the most recent posts, newest first, with only the
// fields the strip and meter need. Nullable insight columns stay pointers so a
// column the platform did not expose is not read as zero.
func (r *metricsRepository) recentPostAggregates(ctx context.Context, influencerID uuid.UUID) ([]model.PostAgg, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT posted_at, like_count, comment_count, share_count,
		       reach_count, save_count, is_sponsored
		FROM post
		WHERE influencer_id = $1
		ORDER BY posted_at DESC NULLS LAST, id
		LIMIT $2`, influencerID, summaryPostWindow)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_posts", "could not read posts")
	}
	defer rows.Close()

	var out []model.PostAgg
	for rows.Next() {
		var (
			p                       model.PostAgg
			likes, comments, shares *int64
		)
		if err := rows.Scan(&p.PostedAt, &likes, &comments, &shares, &p.Reach, &p.Saves, &p.IsSponsored); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "metrics.summary_posts_scan", "could not read posts")
		}
		// The engagement counters are non-null in practice (the connector resolves
		// them to zero on ingest), but tolerate a NULL as zero for the count math.
		p.Likes = deref(likes)
		p.Comments = deref(comments)
		p.Shares = deref(shares)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_posts_rows", "could not read posts")
	}
	return out, nil
}

// commentSampleCount counts the pseudonymized comment samples captured for the
// influencer, for the readiness checklist.
func (r *metricsRepository) commentSampleCount(ctx context.Context, influencerID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM comment_sample cs
		JOIN post p ON p.id = cs.post_id
		WHERE p.influencer_id = $1`, influencerID).Scan(&n)
	if err != nil {
		return 0, errs.Wrap(err, errs.KindUnavailable, "metrics.summary_comment_count", "could not count comment samples")
	}
	return n, nil
}

// FollowerSeries returns the influencer's follower time series for Instagram,
// oldest first, falling back to any platform when Instagram has no points. It
// backs the metrics module's InstagramFollowerSeries facade; it is not an HTTP
// route.
func (r *metricsRepository) FollowerSeries(ctx context.Context, influencerID uuid.UUID) ([]model.FollowerPoint, error) {
	points, err := r.followerSeriesForPlatform(ctx, influencerID, "instagram")
	if err != nil {
		return nil, err
	}
	if len(points) > 0 {
		return points, nil
	}
	return r.followerSeriesForPlatform(ctx, influencerID, "")
}

// followerSeriesForPlatform reads the 'followers' series ordered oldest first. An
// empty platform means "any platform".
func (r *metricsRepository) followerSeriesForPlatform(ctx context.Context, influencerID uuid.UUID, platform string) ([]model.FollowerPoint, error) {
	query := `
		SELECT "time", value FROM metric_point
		WHERE influencer_id = $1 AND metric = 'followers'`
	args := []any{influencerID}
	if platform != "" {
		args = append(args, platform)
		query += ` AND platform = $2`
	}
	query += ` ORDER BY "time" ASC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.follower_series", "could not read follower series")
	}
	defer rows.Close()

	var out []model.FollowerPoint
	for rows.Next() {
		var (
			at time.Time
			v  float64
		)
		if err := rows.Scan(&at, &v); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "metrics.follower_series_scan", "could not read follower series")
		}
		out = append(out, model.FollowerPoint{At: at, Followers: v})
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.follower_series_rows", "could not read follower series")
	}
	return out, nil
}

// deref returns the pointed-to value, or zero for a nil pointer.
func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
