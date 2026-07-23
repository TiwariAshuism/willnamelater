package provider

import (
	"errors"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/oauth/internal/service"
)

// The Meta account resolver reads TWO ids off one call
// (/me?fields=id,accounts{instagram_business_account{id,username}}):
//   - the Instagram Business account id, hanging off a managed Page — the account
//     we audit; and
//   - the top-level `id`, Meta's APP-SCOPED USER ID — the handle Meta's
//     deauthorize and data-deletion callbacks use to name whose data to erase.
//
// Capturing the second at connect time is what makes those callbacks actionable;
// without it a deletion request arrives naming a user we cannot resolve.
//
// These fixtures are shaped after the documented Graph API response, not captured
// user data.
func TestParseAccountMeta(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantAcct   string
		wantUserID string
		wantErr    bool
	}{
		{
			name: "first page with a linked ig business account, plus the app-scoped user id",
			body: `{"id":"1234567890","accounts":{"data":[
				{"instagram_business_account":{"id":"17841400000000001"},"id":"page1"}
			]}}`,
			wantAcct:   "17841400000000001",
			wantUserID: "1234567890",
		},
		{
			name: "skips pages without a linked ig account",
			body: `{"id":"1234567890","accounts":{"data":[
				{"id":"page-no-ig"},
				{"instagram_business_account":{"id":"17841400000000002"},"id":"page2"}
			]}}`,
			wantAcct:   "17841400000000002",
			wantUserID: "1234567890",
		},
		{
			name:    "no linked ig account is an honest error, not a fabricated id",
			body:    `{"id":"1234567890","accounts":{"data":[{"id":"page-no-ig"}]}}`,
			wantErr: true,
		},
		{
			name:    "empty pages list errors",
			body:    `{"id":"1234567890","accounts":{"data":[]}}`,
			wantErr: true,
		},
		{
			name:    "no accounts edge at all errors",
			body:    `{"id":"1234567890"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAccount(service.ProviderMeta, []byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAccount: %v", err)
			}
			if got.AccountID != tt.wantAcct {
				t.Fatalf("account id = %q, want %q", got.AccountID, tt.wantAcct)
			}
			if got.UserID != tt.wantUserID {
				t.Fatalf("app-scoped user id = %q, want %q", got.UserID, tt.wantUserID)
			}
		})
	}
}

// A login with no linked IG business account must surface the distinct sentinel,
// not a generic error, so the service can map it to the guided-fix domain error.
func TestParseAccountMetaMissingIGReturnsSentinel(t *testing.T) {
	_, err := parseAccount(service.ProviderMeta, []byte(`{"id":"1234567890","accounts":{"data":[{"id":"page-no-ig"}]}}`))
	if !errors.Is(err, service.ErrNoInstagramBusinessAccount) {
		t.Fatalf("err = %v, want ErrNoInstagramBusinessAccount", err)
	}
}

// The Instagram username, when the account-info call returns it, is captured as
// the handle the signup flow records for the influencer.
func TestParseAccountMetaCapturesHandle(t *testing.T) {
	got, err := parseAccount(service.ProviderMeta, []byte(
		`{"id":"1234567890","accounts":{"data":[{"instagram_business_account":{"id":"17841400000000001","username":"creator.handle"},"id":"page1"}]}}`))
	if err != nil {
		t.Fatalf("parseAccount: %v", err)
	}
	if got.Handle != "creator.handle" {
		t.Fatalf("handle = %q, want creator.handle", got.Handle)
	}
	if got.AccountID != "17841400000000001" {
		t.Fatalf("account id = %q", got.AccountID)
	}
}

// YouTube has no deauthorize/data-deletion callback keyed on an app-scoped id, so
// the resolver captures the channel id and leaves the user id empty rather than
// inventing one.
func TestParseAccountGoogle(t *testing.T) {
	got, err := parseAccount(service.ProviderGoogle, []byte(`{"items":[{"id":"UC_channel"}]}`))
	if err != nil {
		t.Fatalf("parseAccount: %v", err)
	}
	if got.AccountID != "UC_channel" {
		t.Fatalf("account id = %q, want UC_channel", got.AccountID)
	}
	if got.UserID != "" {
		t.Fatalf("user id = %q, want empty (youtube has no app-scoped callback id)", got.UserID)
	}
}
