package model

import "time"

// ProfileSummaryResponse is the response body of GET
// /influencers/:id/profile-summary. It surfaces data the audit already captured
// for the result page (PRD §8): the audience snapshot, a verified-metrics strip,
// and a media-kit readiness meter.
//
// HONESTY is the whole point of this shape. Every numeric that can be absent is a
// pointer or an omitted map entry: a field with no data is null (strip) / missing
// (audience) / false (readiness), NEVER a fabricated zero. A zero followers count
// or a zero engagement rate is a real, different statement from "we could not
// measure it".
type ProfileSummaryResponse struct {
	InfluencerID string           `json:"influencer_id"`
	Audience     AudienceSnapshot `json:"audience"`
	MetricsStrip MetricsStrip     `json:"metrics_strip"`
	Readiness    Readiness        `json:"readiness"`
}

// AudienceSnapshot holds the most recent audit's demographic distributions. Each
// map is bucket -> fraction in [0,1]; a nil/empty map means that dimension was not
// pulled — an under-100-follower account, a non-Meta platform, or a pull that can
// lag ~48h. Language is deliberately absent: Meta's follower_demographics does not
// expose it, so it is omitted rather than faked.
type AudienceSnapshot struct {
	Age     map[string]float64 `json:"age,omitempty"`
	Gender  map[string]float64 `json:"gender,omitempty"`
	Country map[string]float64 `json:"country,omitempty"`
}

// MetricsStrip is the verified headline strip. Every field is a POINTER: nil means
// "not measured", never zero.
type MetricsStrip struct {
	// Followers is the latest metric_point 'followers' reading.
	Followers *int64 `json:"followers,omitempty"`
	// EngagementRate is the mean per-post (likes+comments+shares)/followers over
	// recent posts. Nil when followers is unknown or non-positive, or there are no
	// posts — a rate cannot be divided by an unknown denominator.
	EngagementRate *float64 `json:"engagement_rate,omitempty"`
	// ReachRatio is the median of metric_point 'reach_ratio' (per-media reach /
	// followers). Nil when no reach_ratio was ever recorded (a public-only pull).
	ReachRatio *float64 `json:"reach_ratio,omitempty"`
	// SaveRate is the median per-post saved/reach over posts where both are known.
	SaveRate *float64 `json:"save_rate,omitempty"`
	// ShareRate is the median per-post shares/reach over posts where reach is known.
	ShareRate *float64 `json:"share_rate,omitempty"`
	// PostingCadenceDays is the median number of days between consecutive recent
	// posts. Nil when fewer than two posts carry a timestamp.
	PostingCadenceDays *float64 `json:"posting_cadence_days,omitempty"`
}

// ReadinessField is one checklist item in the media-kit readiness meter.
type ReadinessField struct {
	Field   string `json:"field"`
	Present bool   `json:"present"`
}

// Readiness is a media-kit completeness METER — never a score. Fraction is the
// share of checklist fields present, in [0,1]; Fields is the per-item checklist.
// A field with no supporting data is Present:false, never a fabricated value.
type Readiness struct {
	Fraction float64          `json:"fraction"`
	Fields   []ReadinessField `json:"fields"`
}

// --- internal ingredients (repository -> aggregation), not serialized ----------

// ProfileSummaryData is the raw material the repository gathers from the
// metrics-owned tables. The aggregation helpers turn it into a
// ProfileSummaryResponse; keeping the fetch and the math separate lets the honest
// nil/false logic be unit-tested without a database.
type ProfileSummaryData struct {
	// Audience buckets for the influencer's most recent audit (one per observed
	// bucket); empty when none were pulled.
	Audience []AudienceBucket
	// Followers is the latest metric_point 'followers' value, nil when never seen.
	Followers *int64
	// ReachRatios are the recorded metric_point 'reach_ratio' values (for a median).
	ReachRatios []float64
	// Posts are the influencer's most recent posts (capped), newest first.
	Posts []PostAgg
	// CommentSampleCount is how many comment_sample rows exist for the influencer.
	CommentSampleCount int
}

// FollowerPoint is one reading in a follower time series. It backs the metrics
// module's InstagramFollowerSeries facade, which a sibling scoring agent consumes.
type FollowerPoint struct {
	At        time.Time
	Followers float64
}

// AudienceBucket is one observed demographic bucket from the most recent audit.
type AudienceBucket struct {
	Dimension string // age | gender | country
	Bucket    string
	Fraction  float64
}

// PostAgg is the subset of a post the metrics strip and readiness meter need. The
// engagement counters are plain int64 (the connector resolves them to zero); the
// insight columns are pointers because the platform may not expose them, and a
// NULL reach must never be read as "reached nobody".
type PostAgg struct {
	PostedAt    *time.Time
	Likes       int64
	Comments    int64
	Shares      int64
	Reach       *int64
	Saves       *int64
	IsSponsored *bool
}
