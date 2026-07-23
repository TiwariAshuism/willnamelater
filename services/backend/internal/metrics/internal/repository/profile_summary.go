package repository

import (
	"math"
	"sort"

	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
)

// readinessMinRecentPosts is the "enough recent posts" threshold for the media-kit
// readiness meter. It is a display heuristic, not a score gate.
const readinessMinRecentPosts = 5

// Readiness checklist field names. A fixed, ordered set: adding a field lowers
// every existing meter proportionally, so the set is deliberately stable.
const (
	readinessProfile     = "profile"
	readinessRecentPosts = "recent_posts"
	readinessAudience    = "audience_demographics"
	readinessSponsored   = "sponsored_history"
	readinessVerified    = "verified_insights"
	readinessCommentBody = "comment_samples"
)

// buildProfileSummary assembles the honest response from raw fetched data. It
// contains no I/O so it is unit-tested directly: every absent input yields a nil
// pointer / omitted map / false checklist item, never a fabricated zero.
func buildProfileSummary(influencerID string, d model.ProfileSummaryData) model.ProfileSummaryResponse {
	return model.ProfileSummaryResponse{
		InfluencerID: influencerID,
		Audience:     buildAudience(d.Audience),
		MetricsStrip: buildMetricsStrip(d),
		Readiness:    buildReadiness(d),
	}
}

// buildAudience folds the observed buckets into per-dimension maps. A dimension
// with no observed bucket stays a nil map (omitted from JSON), never a zero-filled
// distribution.
func buildAudience(buckets []model.AudienceBucket) model.AudienceSnapshot {
	var snap model.AudienceSnapshot
	for _, b := range buckets {
		switch b.Dimension {
		case "age":
			if snap.Age == nil {
				snap.Age = make(map[string]float64)
			}
			snap.Age[b.Bucket] = b.Fraction
		case "gender":
			if snap.Gender == nil {
				snap.Gender = make(map[string]float64)
			}
			snap.Gender[b.Bucket] = b.Fraction
		case "country":
			if snap.Country == nil {
				snap.Country = make(map[string]float64)
			}
			snap.Country[b.Bucket] = b.Fraction
		}
	}
	return snap
}

// buildMetricsStrip computes the verified headline metrics. Each is nil unless it
// can be computed honestly from real data.
func buildMetricsStrip(d model.ProfileSummaryData) model.MetricsStrip {
	strip := model.MetricsStrip{Followers: d.Followers}

	// Engagement rate: mean per-post (likes+comments+shares)/followers. Requires a
	// known, positive follower denominator and at least one post.
	if d.Followers != nil && *d.Followers > 0 && len(d.Posts) > 0 {
		followers := float64(*d.Followers)
		rates := make([]float64, 0, len(d.Posts))
		for _, p := range d.Posts {
			rates = append(rates, float64(p.Likes+p.Comments+p.Shares)/followers)
		}
		if m, ok := mean(rates); ok {
			strip.EngagementRate = &m
		}
	}

	// Reach ratio: median of the recorded reach_ratio series.
	if m, ok := median(d.ReachRatios); ok {
		strip.ReachRatio = &m
	}

	// Save rate: median per-post saved/reach where both are known and reach > 0.
	saveRates := make([]float64, 0, len(d.Posts))
	shareRates := make([]float64, 0, len(d.Posts))
	for _, p := range d.Posts {
		if p.Reach != nil && *p.Reach > 0 {
			reach := float64(*p.Reach)
			shareRates = append(shareRates, float64(p.Shares)/reach)
			if p.Saves != nil {
				saveRates = append(saveRates, float64(*p.Saves)/reach)
			}
		}
	}
	if m, ok := median(saveRates); ok {
		strip.SaveRate = &m
	}
	if m, ok := median(shareRates); ok {
		strip.ShareRate = &m
	}

	// Posting cadence: median days between consecutive timestamped posts.
	if m, ok := median(postingGapsDays(d.Posts)); ok {
		strip.PostingCadenceDays = &m
	}

	return strip
}

// buildReadiness is a completeness METER over a fixed checklist. A field with no
// supporting data is Present:false; the fraction is simply present/total.
func buildReadiness(d model.ProfileSummaryData) model.Readiness {
	sponsored := false
	verified := len(d.ReachRatios) > 0
	for _, p := range d.Posts {
		if p.IsSponsored != nil && *p.IsSponsored {
			sponsored = true
		}
		if p.Reach != nil || p.Saves != nil {
			verified = true
		}
	}

	fields := []model.ReadinessField{
		// We hold this creator's basic footprint (a follower reading or any post).
		{Field: readinessProfile, Present: d.Followers != nil || len(d.Posts) > 0},
		{Field: readinessRecentPosts, Present: len(d.Posts) >= readinessMinRecentPosts},
		{Field: readinessAudience, Present: len(d.Audience) > 0},
		{Field: readinessSponsored, Present: sponsored},
		{Field: readinessVerified, Present: verified},
		{Field: readinessCommentBody, Present: d.CommentSampleCount > 0},
	}

	present := 0
	for _, f := range fields {
		if f.Present {
			present++
		}
	}
	return model.Readiness{
		Fraction: float64(present) / float64(len(fields)),
		Fields:   fields,
	}
}

// postingGapsDays returns the gaps, in days, between consecutive timestamped
// posts. Posts without a timestamp are skipped; fewer than two timestamps yields
// no gaps (so the cadence is nil, never a fabricated 0).
func postingGapsDays(posts []model.PostAgg) []float64 {
	times := make([]float64, 0, len(posts))
	for _, p := range posts {
		if p.PostedAt != nil {
			times = append(times, float64(p.PostedAt.UnixNano()))
		}
	}
	if len(times) < 2 {
		return nil
	}
	sort.Float64s(times)
	const nanosPerDay = float64(24 * 60 * 60 * 1e9)
	gaps := make([]float64, 0, len(times)-1)
	for i := 1; i < len(times); i++ {
		gaps = append(gaps, (times[i]-times[i-1])/nanosPerDay)
	}
	return gaps
}

// mean returns the arithmetic mean and true, or 0 and false for an empty slice.
func mean(xs []float64) (float64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs)), true
}

// median returns the median and true, or 0 and false for an empty slice. It does
// not mutate the caller's slice.
func median(xs []float64) (float64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2], true
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2, true
}

// roundToInt64 rounds a float metric value to the nearest int64. Used for
// follower counts, which are stored as double precision in metric_point.
func roundToInt64(v float64) int64 {
	return int64(math.Round(v))
}
