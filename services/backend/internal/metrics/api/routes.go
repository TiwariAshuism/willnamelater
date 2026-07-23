// Package api is the apigen source for the metrics module: an annotated Go
// interface from which the service and repository interfaces are generated.
//
// The metrics module owns the metric_point time series, the post table, and the
// pseudonymized comment_sample table. Its two read routes expose that data for
// one influencer; the write path (Snapshot ingest) is a service method the audit
// worker calls, not an HTTP route, and so is not declared here.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
)

// MetricsAPI is the metrics module's HTTP surface.
type MetricsAPI interface {
	// GET /influencers/:id/metrics
	GetInfluencerMetrics(ctx context.Context, id string, req model.MetricSeriesRequest) (model.MetricSeriesResponse, error)

	// GET /influencers/:id/posts
	ListInfluencerPosts(ctx context.Context, id string, req model.ListPostsRequest) ([]model.PostResponse, error)

	// GET /influencers/:id/profile-summary
	GetInfluencerProfileSummary(ctx context.Context, id string) (model.ProfileSummaryResponse, error)
}
