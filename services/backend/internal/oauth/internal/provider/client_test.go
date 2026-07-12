package provider

import (
	"testing"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
)

// The Meta account resolver reads the Instagram Business account id off the
// user's managed Pages (/me/accounts?fields=instagram_business_account{id}),
// not the Facebook user id. These fixtures are shaped after the documented Graph
// API response, not captured user data.
func TestParseAccountIDMeta(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{
			name: "first page with a linked ig business account",
			body: `{"data":[
				{"instagram_business_account":{"id":"17841400000000001"},"id":"page1"}
			]}`,
			want: "17841400000000001",
		},
		{
			name: "skips pages without a linked ig account",
			body: `{"data":[
				{"id":"page-no-ig"},
				{"instagram_business_account":{"id":"17841400000000002"},"id":"page2"}
			]}`,
			want: "17841400000000002",
		},
		{
			name:    "no linked ig account is an honest error, not a fabricated id",
			body:    `{"data":[{"id":"page-no-ig"}]}`,
			wantErr: true,
		},
		{
			name:    "empty pages list errors",
			body:    `{"data":[]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAccountID(service.ProviderMeta, []byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got id %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAccountID: %v", err)
			}
			if got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
		})
	}
}
