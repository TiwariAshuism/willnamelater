package model

import (
	"testing"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

func TestParseNiche(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    Niche
		wantErr bool
	}{
		{name: "known category", raw: "beauty", want: NicheBeauty},
		{name: "catch-all other", raw: "other", want: NicheOther},
		{name: "empty is invalid", raw: "", wantErr: true},
		{name: "unknown is invalid", raw: "crypto", wantErr: true},
		{name: "case sensitive", raw: "Beauty", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseNiche(tt.raw)
			if tt.wantErr {
				if errs.KindOf(err) != errs.KindInvalid {
					t.Fatalf("ParseNiche(%q) kind = %v, want KindInvalid", tt.raw, errs.KindOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNiche(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParseNiche(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParsePlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    connector.Platform
		wantErr bool
	}{
		{name: "youtube", raw: "youtube", want: connector.PlatformYouTube},
		{name: "instagram", raw: "instagram", want: connector.PlatformInstagram},
		{name: "x", raw: "x", want: connector.PlatformX},
		{name: "empty is invalid", raw: "", wantErr: true},
		{name: "unknown network is invalid", raw: "myspace", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePlatform(tt.raw)
			if tt.wantErr {
				if errs.KindOf(err) != errs.KindInvalid {
					t.Fatalf("ParsePlatform(%q) kind = %v, want KindInvalid", tt.raw, errs.KindOf(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePlatform(%q) unexpected error: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("ParsePlatform(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
