package repository

import (
	"testing"
	"time"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
)

func i64(v int64) *int64 { return &v }
func boolp(v bool) *bool { return &v }
func day(n int) *time.Time {
	t := time.Date(2026, 5, n, 0, 0, 0, 0, time.UTC)
	return &t
}

// TestBuildProfileSummaryEmptyIsAllAbsent pins the honesty contract: with no data,
// every strip metric is nil, every audience map is omitted, and no readiness field
// is present — absence is disclosed, never a fabricated zero.
func TestBuildProfileSummaryEmptyIsAllAbsent(t *testing.T) {
	got := buildProfileSummary("inf-1", model.ProfileSummaryData{})

	if got.InfluencerID != "inf-1" {
		t.Fatalf("influencer id = %q", got.InfluencerID)
	}
	s := got.MetricsStrip
	if s.Followers != nil || s.EngagementRate != nil || s.ReachRatio != nil ||
		s.SaveRate != nil || s.ShareRate != nil || s.PostingCadenceDays != nil {
		t.Fatalf("empty data must yield an all-nil strip, got %+v", s)
	}
	if got.Audience.Age != nil || got.Audience.Gender != nil || got.Audience.Country != nil {
		t.Fatalf("empty data must yield no audience maps, got %+v", got.Audience)
	}
	if got.Readiness.Fraction != 0 {
		t.Fatalf("readiness fraction = %v, want 0", got.Readiness.Fraction)
	}
	for _, f := range got.Readiness.Fields {
		if f.Present {
			t.Fatalf("field %q present with no data", f.Field)
		}
	}
}

// TestBuildMetricsStripHonestComputation checks each metric is computed from real
// data and, critically, that engagement rate is nil when followers are unknown —
// a rate cannot be divided by an unknown denominator.
func TestBuildMetricsStripHonestComputation(t *testing.T) {
	data := model.ProfileSummaryData{
		Followers:   i64(1000),
		ReachRatios: []float64{0.2, 0.4, 0.6}, // median 0.4
		Posts: []model.PostAgg{
			// engagement (l+c+s)/1000: (50+30+20)/1000 = 0.1 ; (10+10+0)/1000 = 0.02 -> mean 0.06
			{Likes: 50, Comments: 30, Shares: 20, Reach: i64(500), Saves: i64(50)}, // save 0.1, share 0.04
			{Likes: 10, Comments: 10, Shares: 0, Reach: i64(1000), Saves: i64(50)}, // save 0.05, share 0.0
		},
	}
	s := buildMetricsStrip(data)

	if s.Followers == nil || *s.Followers != 1000 {
		t.Fatalf("followers = %v", s.Followers)
	}
	if s.EngagementRate == nil || !approx(*s.EngagementRate, 0.06) {
		t.Fatalf("engagement rate = %v, want ~0.06", s.EngagementRate)
	}
	if s.ReachRatio == nil || !approx(*s.ReachRatio, 0.4) {
		t.Fatalf("reach ratio = %v, want 0.4", s.ReachRatio)
	}
	if s.SaveRate == nil || !approx(*s.SaveRate, 0.075) { // median of 0.1, 0.05
		t.Fatalf("save rate = %v, want 0.075", s.SaveRate)
	}
	if s.ShareRate == nil || !approx(*s.ShareRate, 0.02) { // median of 0.04, 0.0
		t.Fatalf("share rate = %v, want 0.02", s.ShareRate)
	}
}

// TestEngagementRateNilWithoutFollowers is the key honesty case: real posts but no
// follower denominator must NOT produce a fabricated engagement rate.
func TestEngagementRateNilWithoutFollowers(t *testing.T) {
	data := model.ProfileSummaryData{
		Posts: []model.PostAgg{{Likes: 50, Comments: 30, Shares: 20}},
	}
	if s := buildMetricsStrip(data); s.EngagementRate != nil {
		t.Fatalf("engagement rate must be nil without followers, got %v", *s.EngagementRate)
	}
}

// TestSaveShareRateSkipNullReach proves an insight that the platform did not expose
// (NULL reach) is excluded, never treated as a zero denominator.
func TestSaveShareRateSkipNullReach(t *testing.T) {
	data := model.ProfileSummaryData{
		Posts: []model.PostAgg{
			{Likes: 1, Shares: 5, Reach: nil, Saves: i64(10)}, // no reach -> excluded
		},
	}
	s := buildMetricsStrip(data)
	if s.SaveRate != nil || s.ShareRate != nil {
		t.Fatalf("rates over NULL reach must be nil, got save=%v share=%v", s.SaveRate, s.ShareRate)
	}
}

// TestPostingCadenceNeedsTwoTimestamps: a single timestamped post yields no cadence.
func TestPostingCadenceNeedsTwoTimestamps(t *testing.T) {
	one := buildMetricsStrip(model.ProfileSummaryData{Posts: []model.PostAgg{{PostedAt: day(1)}}})
	if one.PostingCadenceDays != nil {
		t.Fatalf("cadence with one post must be nil, got %v", *one.PostingCadenceDays)
	}
	// Posts 3 days apart then 1 day apart -> gaps {3,1} -> median 2.
	three := buildMetricsStrip(model.ProfileSummaryData{Posts: []model.PostAgg{
		{PostedAt: day(1)}, {PostedAt: day(4)}, {PostedAt: day(5)},
	}})
	if three.PostingCadenceDays == nil || !approx(*three.PostingCadenceDays, 2) {
		t.Fatalf("cadence = %v, want 2", three.PostingCadenceDays)
	}
}

func TestBuildAudienceOmitsUnpulledDimensions(t *testing.T) {
	got := buildAudience([]model.AudienceBucket{
		{Dimension: "age", Bucket: "18-24", Fraction: 0.5},
		{Dimension: "gender", Bucket: "female", Fraction: 0.7},
		// no country buckets -> Country stays nil
	})
	if got.Age["18-24"] != 0.5 || got.Gender["female"] != 0.7 {
		t.Fatalf("audience maps wrong: %+v", got)
	}
	if got.Country != nil {
		t.Fatalf("unpulled country dimension must be a nil map, got %+v", got.Country)
	}
}

func TestBuildReadinessMeter(t *testing.T) {
	// A well-populated creator: followers, 5+ posts, audience, a sponsored post,
	// verified insights, and comment samples -> all six fields present.
	posts := make([]model.PostAgg, 5)
	for i := range posts {
		posts[i] = model.PostAgg{Likes: 1, Reach: i64(100)}
	}
	posts[0].IsSponsored = boolp(true)
	data := model.ProfileSummaryData{
		Followers:          i64(1000),
		Posts:              posts,
		Audience:           []model.AudienceBucket{{Dimension: "age", Bucket: "18-24", Fraction: 1}},
		CommentSampleCount: 3,
	}
	r := buildReadiness(data)
	if !approx(r.Fraction, 1.0) {
		t.Fatalf("fraction = %v, want 1.0 (fields %+v)", r.Fraction, r.Fields)
	}

	// Sparse: only two untimestamped posts, no followers/audience/insights.
	sparse := buildReadiness(model.ProfileSummaryData{Posts: []model.PostAgg{{}, {}}})
	present := map[string]bool{}
	for _, f := range sparse.Fields {
		present[f.Field] = f.Present
	}
	if !present[readinessProfile] {
		t.Fatalf("profile should be present when posts exist")
	}
	if present[readinessRecentPosts] || present[readinessAudience] ||
		present[readinessSponsored] || present[readinessVerified] || present[readinessCommentBody] {
		t.Fatalf("sparse creator over-reported readiness: %+v", sparse.Fields)
	}
}

func TestMedianAndMean(t *testing.T) {
	if _, ok := median(nil); ok {
		t.Fatal("median of empty must report absence")
	}
	if _, ok := mean(nil); ok {
		t.Fatal("mean of empty must report absence")
	}
	if m, _ := median([]float64{3, 1, 2}); m != 2 {
		t.Fatalf("median odd = %v, want 2", m)
	}
	if m, _ := median([]float64{1, 2, 3, 4}); m != 2.5 {
		t.Fatalf("median even = %v, want 2.5", m)
	}
	// median must not mutate its input.
	in := []float64{3, 1, 2}
	_, _ = median(in)
	if in[0] != 3 {
		t.Fatalf("median mutated its input: %v", in)
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}
