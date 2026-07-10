package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Point and post read caps. A request may ask for fewer, never more: an
// unbounded query against a hypertable is a denial-of-service on our own
// database, so an out-of-range or absent Limit is clamped to the default.
const (
	defaultSeriesPoints = 1000
	maxSeriesPoints     = 10000
	defaultPostLimit    = 50
	maxPostLimit        = 500
)

// metricsRepository is the read-side implementation backed by the shared pool.
type metricsRepository struct {
	pool *db.Pool
}

// NewMetricsRepository builds the read repository over pool.
func NewMetricsRepository(pool *db.Pool) MetricsRepository {
	return &metricsRepository{pool: pool}
}

var _ MetricsRepository = (*metricsRepository)(nil)

// GetInfluencerMetrics returns the requested metric series for one influencer.
// Points are capped per (platform, metric) series with a window function so a
// single high-frequency metric cannot starve the others out of the result.
func (r *metricsRepository) GetInfluencerMetrics(ctx context.Context, id string, req model.MetricSeriesRequest) (model.MetricSeriesResponse, error) {
	influencerID, err := uuid.Parse(id)
	if err != nil {
		return model.MetricSeriesResponse{}, errs.Wrap(err, errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")
	}

	args := []any{influencerID}
	conds := []string{`influencer_id = $1`}
	if req.Platform != "" {
		args = append(args, req.Platform)
		conds = append(conds, fmt.Sprintf(`platform = $%d`, len(args)))
	}
	if req.Metric != "" {
		args = append(args, req.Metric)
		conds = append(conds, fmt.Sprintf(`metric = $%d`, len(args)))
	}
	if !req.From.IsZero() {
		args = append(args, req.From)
		conds = append(conds, fmt.Sprintf(`"time" >= $%d`, len(args)))
	}
	if !req.To.IsZero() {
		args = append(args, req.To)
		conds = append(conds, fmt.Sprintf(`"time" < $%d`, len(args)))
	}

	limit := req.Limit
	if limit <= 0 || limit > maxSeriesPoints {
		limit = defaultSeriesPoints
	}
	args = append(args, limit)
	limitPos := len(args)

	query := fmt.Sprintf(`
		SELECT platform, metric, "time", value
		FROM (
			SELECT platform, metric, "time", value,
			       row_number() OVER (PARTITION BY platform, metric ORDER BY "time" DESC) AS rn
			FROM metric_point
			WHERE %s
		) ranked
		WHERE rn <= $%d
		ORDER BY platform, metric, "time"`,
		strings.Join(conds, " AND "), limitPos)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return model.MetricSeriesResponse{}, errs.Wrap(err, errs.KindUnavailable, "metrics.query_metrics", "could not read metric series")
	}
	defer rows.Close()

	resp := model.MetricSeriesResponse{InfluencerID: influencerID.String()}
	idx := -1
	for rows.Next() {
		var platform, metric string
		var at time.Time
		var value float64
		if err := rows.Scan(&platform, &metric, &at, &value); err != nil {
			return model.MetricSeriesResponse{}, errs.Wrap(err, errs.KindInternal, "metrics.scan_metrics", "could not read metric series")
		}
		if idx < 0 || resp.Series[idx].Platform != platform || resp.Series[idx].Metric != metric {
			resp.Series = append(resp.Series, model.MetricSeries{Platform: platform, Metric: metric})
			idx = len(resp.Series) - 1
		}
		resp.Series[idx].Points = append(resp.Series[idx].Points, model.MetricSample{At: at, Value: value})
	}
	if err := rows.Err(); err != nil {
		return model.MetricSeriesResponse{}, errs.Wrap(err, errs.KindUnavailable, "metrics.rows_metrics", "could not read metric series")
	}
	return resp, nil
}

// ListInfluencerPosts returns an influencer's captured posts, newest first.
// Nullable insight columns are scanned into pointer fields so a column the
// platform did not expose stays null rather than collapsing to zero.
func (r *metricsRepository) ListInfluencerPosts(ctx context.Context, id string, req model.ListPostsRequest) ([]model.PostResponse, error) {
	influencerID, err := uuid.Parse(id)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindInvalid, "metrics.invalid_influencer_id", "influencer id must be a uuid")
	}

	args := []any{influencerID}
	conds := []string{`influencer_id = $1`}
	if req.Platform != "" {
		args = append(args, req.Platform)
		conds = append(conds, fmt.Sprintf(`platform = $%d`, len(args)))
	}
	if !req.Since.IsZero() {
		args = append(args, req.Since)
		conds = append(conds, fmt.Sprintf(`posted_at >= $%d`, len(args)))
	}

	limit := req.Limit
	if limit <= 0 || limit > maxPostLimit {
		limit = defaultPostLimit
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	limitPos, offsetPos := len(args)-1, len(args)

	query := fmt.Sprintf(`
		SELECT id, platform, platform_post_id, posted_at, permalink, caption, media_type,
		       like_count, comment_count, share_count, view_count,
		       reach_count, impression_count, save_count, engagement_rate, is_sponsored
		FROM post
		WHERE %s
		ORDER BY posted_at DESC NULLS LAST, id
		LIMIT $%d OFFSET $%d`,
		strings.Join(conds, " AND "), limitPos, offsetPos)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.query_posts", "could not read posts")
	}
	defer rows.Close()

	out := make([]model.PostResponse, 0)
	for rows.Next() {
		var (
			p   model.PostResponse
			pid uuid.UUID
		)
		if err := rows.Scan(
			&pid, &p.Platform, &p.PlatformPostID, &p.PostedAt, &p.Permalink, &p.Caption, &p.MediaType,
			&p.LikeCount, &p.CommentCount, &p.ShareCount, &p.ViewCount,
			&p.ReachCount, &p.ImpressionCount, &p.SaveCount, &p.EngagementRate, &p.IsSponsored,
		); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "metrics.scan_posts", "could not read posts")
		}
		p.ID = pid.String()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "metrics.rows_posts", "could not read posts")
	}
	return out, nil
}
