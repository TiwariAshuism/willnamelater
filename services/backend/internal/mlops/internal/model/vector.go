package model

import "encoding/json"

// FeatureVector is the frozen feature vector computed once at capture (Go) and
// read verbatim by the Python trainer — the feature store's whole purpose is to
// eliminate train/serve skew in feature computation. The JSON key order below is
// frozen and matches the resolved contract; the fraud sub-vector's six keys equal
// the trainer's existing FEATURE_ORDER.
//
// Optional descriptive features are pointers so an absent feature marshals to
// JSON null (encoding/json emits null for a nil pointer with no omitempty),
// never a fabricated zero. LightGBM consumes JSON null as native missing/NaN.
// FollowingCount and Verified are always nil today: connector.Snapshot does not
// yet carry a following count or a verified flag (a flagged foundation gap), and
// the no-zero-fill rule forbids inventing them.
type FeatureVector struct {
	// Fraud sub-vector — copied verbatim from the audit's fraud_result. Keys equal
	// the trainer's FEATURE_ORDER; always present.
	FakeFollowerRate         float64 `json:"fake_follower_rate"`
	BotCommentRate           float64 `json:"bot_comment_rate"`
	EngagementAnomaly        float64 `json:"engagement_anomaly"`
	CliqueCount              int     `json:"clique_count"`
	CliqueMembershipFraction float64 `json:"clique_membership_fraction"`
	Confidence               float64 `json:"confidence"`

	// Descriptive / reach sub-vector — computed at capture from the snapshot.
	FollowerCount          int64    `json:"follower_count"`
	FollowingCount         *int64   `json:"following_count"`
	FollowerFollowingRatio *float64 `json:"follower_following_ratio"`
	EngagementRate         *float64 `json:"engagement_rate"`
	EngagementRateVariance *float64 `json:"engagement_rate_variance"`
	CommentLikeRatio       *float64 `json:"comment_like_ratio"`
	PostingCadencePerWeek  *float64 `json:"posting_cadence_per_week"`
	AccountAgeDaysProxy    *float64 `json:"account_age_days_proxy"`
	PostCount              int      `json:"post_count"`
	Niche                  string   `json:"niche"`
	Tier                   string   `json:"tier"`
	Verified               *bool    `json:"verified"`
	Platform               string   `json:"platform"`
}

// Marshal serialises the vector to its stored jsonb form, preserving the frozen
// key order and JSON nulls for absent features.
func (v FeatureVector) Marshal() (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
