package ml

import "time"

// The types below mirror the pydantic v2 models in services/ml/app/schemas.py,
// which are the single source of truth for the wire contract. Field names and
// JSON tags are copied verbatim from that file; changing one here without
// changing the other silently breaks the contract (the ML service parses with
// extra="forbid" and strict typing, so a drifted field surfaces as a 422/400).

// Platform mirrors app.schemas.Platform: the source platform of the audited
// account. Values are lowercase and identical to connector.Platform, so a
// connector.Snapshot's platform maps across with a plain string conversion.
type Platform string

// The Platform values below mirror app.schemas.Platform verbatim.
const (
	PlatformInstagram Platform = "instagram"
	PlatformYouTube   Platform = "youtube"
	PlatformTikTok    Platform = "tiktok"
	PlatformX         Platform = "x"
	PlatformFacebook  Platform = "facebook"
	PlatformLinkedIn  Platform = "linkedin"
)

// SignalContribution mirrors app.schemas.SignalContribution: one explainable
// signal and how much it moved the composite score.
type SignalContribution struct {
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	Weight   float64 `json:"weight"`
	Weighted float64 `json:"weighted"`
	Detail   string  `json:"detail"`
}

// FollowerPoint mirrors app.schemas.FollowerPoint: one observation in an
// account's follower-count time series.
type FollowerPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int64     `json:"count"`
}

// PostMetrics mirrors app.schemas.PostMetrics: public engagement counters for
// one post. PostID is the join key (a CommentEvent carries the same id) that
// lets comments be attributed to the post they were left on; the schema
// requires it (min_length=1). Views is a pointer because the schema field is
// `int | None`; a nil pointer marshals to JSON null, which the service accepts
// as "not reported".
type PostMetrics struct {
	PostID    string    `json:"post_id"`
	Timestamp time.Time `json:"timestamp"`
	Likes     int64     `json:"likes"`
	Comments  int64     `json:"comments"`
	Views     *int64    `json:"views"`
}

// EngagementBenchmarkPoint mirrors app.schemas.EngagementBenchmarkPoint: one
// (follower-threshold, expected-rate) knot of a sourced benchmark curve.
type EngagementBenchmarkPoint struct {
	FollowerThreshold int64   `json:"follower_threshold"`
	ExpectedRate      float64 `json:"expected_rate"`
}

// EngagementBenchmark mirrors app.schemas.EngagementBenchmark: a sourced
// expected-engagement curve supplied by the caller. The ML service owns no
// benchmarks; the Go scoring module reads them from the versioned benchmark
// table and passes them in with a provenance source label. When absent (a nil
// pointer marshals to JSON null) the engagement-deviation signal contributes
// nothing rather than being anchored to a guessed curve.
type EngagementBenchmark struct {
	Curve  []EngagementBenchmarkPoint `json:"curve"`
	Floor  float64                    `json:"floor"`
	Source string                     `json:"source"`
}

// AccountSnapshot mirrors app.schemas.AccountSnapshot: point-in-time account
// totals.
type AccountSnapshot struct {
	Handle         string   `json:"handle"`
	Platform       Platform `json:"platform"`
	FollowerCount  int64    `json:"follower_count"`
	FollowingCount int64    `json:"following_count"`
}

// FraudScoreRequest mirrors app.schemas.FraudScoreRequest: everything the fraud
// scorer needs, drawn entirely from the request. The service loads no history;
// the follower series and posts here are the only data its per-call models see.
// EngagementBenchmark is optional (nil marshals to null); when set the caller
// supplies a sourced expected-engagement curve for the deviation signal.
type FraudScoreRequest struct {
	Account             AccountSnapshot      `json:"account"`
	FollowerSeries      []FollowerPoint      `json:"follower_series"`
	Posts               []PostMetrics        `json:"posts"`
	EngagementBenchmark *EngagementBenchmark `json:"engagement_benchmark"`
}

// FraudScoreResponse mirrors app.schemas.FraudScoreResponse: an authenticity
// risk estimate for one account. Score runs 0-100 where higher means more
// likely inauthentic; it is an estimate, never a measured fake-follower rate.
type FraudScoreResponse struct {
	Score        float64              `json:"score"`
	Confidence   float64              `json:"confidence"`
	ModelVersion string               `json:"model_version"`
	Estimate     bool                 `json:"estimate"`
	Signals      []SignalContribution `json:"signals"`
	Flags        []string             `json:"flags"`
	GeneratedAt  time.Time            `json:"generated_at"`
}

// CommentEvent mirrors app.schemas.CommentEvent: one commenter appearing on one
// post. PostID is what lets the service join a comment to its post and build the
// co-commenter graph. Text is carried as a wire-contract slot for future
// verified text signals; the current clique model joins purely on post_id and
// commenter and does not read it.
type CommentEvent struct {
	PostID    string    `json:"post_id"`
	Commenter string    `json:"commenter"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// PodsDetectRequest mirrors app.schemas.PodsDetectRequest: comment events plus
// the parameters of the co-commenter clique model. Each edge is weighted by the
// number of shared posts between two commenters, so no time window is used:
// MinSharedPosts prunes weak edges and MinPodSize is the clique size that counts
// as coordination.
type PodsDetectRequest struct {
	Events         []CommentEvent `json:"events"`
	MinPodSize     int            `json:"min_pod_size"`
	MinSharedPosts int            `json:"min_shared_posts"`
}

// Pod mirrors app.schemas.Pod: a maximal clique of commenters who co-comment on
// many shared posts.
type Pod struct {
	Members    []string `json:"members"`
	Size       int      `json:"size"`
	Cohesion   float64  `json:"cohesion"`
	Confidence float64  `json:"confidence"`
}

// PodsDetectResponse mirrors app.schemas.PodsDetectResponse. CliqueCount
// (maximal cliques of size >= MinPodSize) is the primary coordination signal;
// CliqueMembershipFraction is secondary. Partial is true when the graph had to
// be reduced to stay inside the compute budget, in which case CliqueCount is a
// lower bound.
type PodsDetectResponse struct {
	Pods                     []Pod                `json:"pods"`
	CliqueCount              int                  `json:"clique_count"`
	CliqueMembershipFraction float64              `json:"clique_membership_fraction"`
	CommentersAnalyzed       int                  `json:"commenters_analyzed"`
	Partial                  bool                 `json:"partial"`
	Signals                  []SignalContribution `json:"signals"`
	Confidence               float64              `json:"confidence"`
	ModelVersion             string               `json:"model_version"`
	Estimate                 bool                 `json:"estimate"`
	GeneratedAt              time.Time            `json:"generated_at"`
}

// CommentLabel mirrors app.schemas.CommentLabel: the heuristic quality bucket
// for a single comment.
type CommentLabel string

// The CommentLabel values below mirror app.schemas.CommentLabel verbatim.
const (
	CommentLabelGenuine   CommentLabel = "genuine"
	CommentLabelGeneric   CommentLabel = "generic"
	CommentLabelEmojiOnly CommentLabel = "emoji_only"
	CommentLabelDuplicate CommentLabel = "duplicate"
)

// CommentItem mirrors app.schemas.CommentItem: one comment to classify.
type CommentItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// CommentsClassifyRequest mirrors app.schemas.CommentsClassifyRequest.
type CommentsClassifyRequest struct {
	Comments []CommentItem `json:"comments"`
}

// CommentClassification mirrors app.schemas.CommentClassification.
type CommentClassification struct {
	ID         string       `json:"id"`
	Label      CommentLabel `json:"label"`
	Confidence float64      `json:"confidence"`
	Signals    []string     `json:"signals"`
}

// CommentsClassifyResponse mirrors app.schemas.CommentsClassifyResponse.
type CommentsClassifyResponse struct {
	Classifications []CommentClassification `json:"classifications"`
	LowQualityRatio float64                 `json:"low_quality_ratio"`
	Confidence      float64                 `json:"confidence"`
	ModelVersion    string                  `json:"model_version"`
	Estimate        bool                    `json:"estimate"`
	GeneratedAt     time.Time               `json:"generated_at"`
}

// errorEnvelope mirrors app.schemas.ErrorResponse: the {code, message} envelope
// the ML service returns on a non-2xx response, matching the Go errs envelope.
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
