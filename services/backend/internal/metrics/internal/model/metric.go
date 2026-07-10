// Package model holds the metrics module's request/response DTOs and the
// row types exchanged between its service and repository layers.
package model

import "time"

// MetricSeriesRequest bounds a time-series query for one influencer. Every
// field is optional; the zero value returns every recorded metric across every
// platform with the repository's default point cap applied.
type MetricSeriesRequest struct {
	// Metric filters to a single metric name (e.g. "followers"). Empty returns
	// every metric.
	Metric string `form:"metric"`
	// Platform filters to a single platform value (e.g. "youtube"). Empty
	// returns every platform.
	Platform string `form:"platform"`
	// From lower-bounds the returned points, inclusive. A zero time means no
	// lower bound.
	From time.Time `form:"from" time_format:"2006-01-02T15:04:05Z07:00"`
	// To upper-bounds the returned points, exclusive. A zero time means no upper
	// bound.
	To time.Time `form:"to" time_format:"2006-01-02T15:04:05Z07:00"`
	// Limit caps the number of points returned per series. Zero or negative
	// applies the repository default.
	Limit int `form:"limit"`
}

// MetricSample is one time-stamped reading within a series.
type MetricSample struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// MetricSeries is the ordered set of samples for one (platform, metric) pair.
type MetricSeries struct {
	Platform string         `json:"platform"`
	Metric   string         `json:"metric"`
	Points   []MetricSample `json:"points"`
}

// MetricSeriesResponse is the response body of GET /influencers/:id/metrics.
type MetricSeriesResponse struct {
	InfluencerID string         `json:"influencer_id"`
	Series       []MetricSeries `json:"series"`
}

// ListPostsRequest bounds a listing of an influencer's captured posts.
type ListPostsRequest struct {
	// Platform filters to a single platform value. Empty returns every platform.
	Platform string `form:"platform"`
	// Since lower-bounds posted_at, inclusive. A zero time means no lower bound.
	Since time.Time `form:"since" time_format:"2006-01-02T15:04:05Z07:00"`
	// Limit caps how many posts are returned. Zero or negative applies the
	// repository default.
	Limit int `form:"limit"`
	// Offset skips this many posts for pagination. Negative is treated as zero.
	Offset int `form:"offset"`
}

// PostResponse is one post in the response of GET /influencers/:id/posts.
//
// Counters the platform did not expose are null, never zero: a null reach on an
// unconnected account must not be reported as "reached nobody". Pointer fields
// carry that distinction to the client.
type PostResponse struct {
	ID              string     `json:"id"`
	Platform        string     `json:"platform"`
	PlatformPostID  string     `json:"platform_post_id"`
	PostedAt        *time.Time `json:"posted_at,omitempty"`
	Permalink       *string    `json:"permalink,omitempty"`
	Caption         *string    `json:"caption,omitempty"`
	MediaType       *string    `json:"media_type,omitempty"`
	LikeCount       *int64     `json:"like_count,omitempty"`
	CommentCount    *int64     `json:"comment_count,omitempty"`
	ShareCount      *int64     `json:"share_count,omitempty"`
	ViewCount       *int64     `json:"view_count,omitempty"`
	ReachCount      *int64     `json:"reach_count,omitempty"`
	ImpressionCount *int64     `json:"impression_count,omitempty"`
	SaveCount       *int64     `json:"save_count,omitempty"`
	EngagementRate  *float64   `json:"engagement_rate,omitempty"`
	IsSponsored     *bool      `json:"is_sponsored,omitempty"`
}
