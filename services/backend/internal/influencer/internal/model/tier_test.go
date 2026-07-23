package model

import "testing"

func TestTierForFollowers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		followers int64
		want      Tier
	}{
		{name: "negative treated as nano", followers: -1, want: TierNano},
		{name: "zero is nano", followers: 0, want: TierNano},
		{name: "just below micro floor is nano", followers: 9_999, want: TierNano},
		{name: "micro floor inclusive", followers: 10_000, want: TierMicro},
		{name: "just below mid floor is micro", followers: 99_999, want: TierMicro},
		{name: "mid floor inclusive", followers: 100_000, want: TierMid},
		{name: "just below macro floor is mid", followers: 499_999, want: TierMid},
		{name: "macro floor inclusive", followers: 500_000, want: TierMacro},
		{name: "just below mega floor is macro", followers: 999_999, want: TierMacro},
		{name: "mega floor inclusive", followers: 1_000_000, want: TierMega},
		{name: "well above mega floor is mega", followers: 25_000_000, want: TierMega},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := TierForFollowers(tt.followers); got != tt.want {
				t.Fatalf("TierForFollowers(%d) = %q, want %q", tt.followers, got, tt.want)
			}
		})
	}
}

func TestTierValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tier Tier
		want bool
	}{
		{tier: TierNano, want: true},
		{tier: TierMicro, want: true},
		{tier: TierMid, want: true},
		{tier: TierMacro, want: true},
		{tier: TierMega, want: true},
		{tier: Tier(""), want: false},
		{tier: Tier("huge"), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.tier), func(t *testing.T) {
			t.Parallel()
			if got := tt.tier.Valid(); got != tt.want {
				t.Fatalf("Tier(%q).Valid() = %v, want %v", tt.tier, got, tt.want)
			}
		})
	}
}
