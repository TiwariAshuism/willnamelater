package app

import "testing"

// connAccountID decides which id a connected-platform fetch queries: the
// OAuth-resolved id (authoritative for a live connection) when present, else the
// influencer handle's own id (the public/CSV cold-start path).
func TestConnAccountID(t *testing.T) {
	tests := []struct {
		name     string
		handleID string
		liveID   string
		want     string
	}{
		{"live id overrides the handle id", "public-handle-id", "17841400000000001", "17841400000000001"},
		{"no live id keeps the handle id", "public-handle-id", "", "public-handle-id"},
		{"live id used even when handle id is empty", "", "17841400000000001", "17841400000000001"},
		{"both empty stays empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connAccountID(tt.handleID, tt.liveID); got != tt.want {
				t.Fatalf("connAccountID(%q, %q) = %q, want %q", tt.handleID, tt.liveID, got, tt.want)
			}
		})
	}
}
