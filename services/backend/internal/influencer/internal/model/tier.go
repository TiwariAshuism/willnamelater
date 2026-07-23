package model

// Tier is an influencer's audience-size band. It keys scoring weights and
// benchmark tables downstream, so its values are stable and lowercase.
type Tier string

// The Tier bands below are the conventional influencer-marketing size buckets
// used across the industry (e.g. Later, HypeAuditor, Mediakix creator-tier
// guidance). They are ordered smallest to largest and are exhaustive over the
// non-negative follower range.
const (
	// TierNano is a creator below the micro threshold. Nano creators are
	// conventionally cited from ~1K followers; anything smaller has no smaller
	// bucket and is still classified here.
	TierNano Tier = "nano"
	// TierMicro is a creator in [10K, 100K) followers.
	TierMicro Tier = "micro"
	// TierMid is a creator in [100K, 500K) followers.
	TierMid Tier = "mid"
	// TierMacro is a creator in [500K, 1M) followers.
	TierMacro Tier = "macro"
	// TierMega is a creator with 1M or more followers.
	TierMega Tier = "mega"
)

// Tier band boundaries, in followers. Each bound is the inclusive lower edge of
// the named band; the band runs up to (but excluding) the next bound. These are
// the conventional cut points used across influencer-marketing tooling.
const (
	microFloor = 10_000
	midFloor   = 100_000
	macroFloor = 500_000
	megaFloor  = 1_000_000
)

// TierForFollowers derives the Tier from a follower count. It is a pure,
// total function: every non-negative count maps to exactly one Tier, and a
// negative count (which the platform never produces) is treated as nano.
//
// Boundaries are half-open on the upper edge, so the named floor is inclusive
// and the next floor is exclusive:
//
//	nano:  followers <  10_000
//	micro: 10_000    <= followers <   100_000
//	mid:   100_000   <= followers <   500_000
//	macro: 500_000   <= followers < 1_000_000
//	mega:  followers >= 1_000_000
//
// Tier is derived here rather than accepted from the client so a caller cannot
// mislabel an account to move it into a more favorable benchmark cohort.
func TierForFollowers(followers int64) Tier {
	switch {
	case followers >= megaFloor:
		return TierMega
	case followers >= macroFloor:
		return TierMacro
	case followers >= midFloor:
		return TierMid
	case followers >= microFloor:
		return TierMicro
	default:
		return TierNano
	}
}

// Valid reports whether t is one of the defined bands.
func (t Tier) Valid() bool {
	switch t {
	case TierNano, TierMicro, TierMid, TierMacro, TierMega:
		return true
	default:
		return false
	}
}
