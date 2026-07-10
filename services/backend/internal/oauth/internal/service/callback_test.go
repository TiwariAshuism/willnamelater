package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeStateStore models the real contract: Consume fetches AND deletes in one
// step, so a state can be used at most once. A fake that merely read would let
// a replay test pass against a store that never deletes.
type fakeStateStore struct {
	mu     sync.Mutex
	states map[string]model.StateData
	err    error
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{states: map[string]model.StateData{}}
}

func (f *fakeStateStore) Save(_ context.Context, state string, data model.StateData, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[state] = data
	return nil
}

func (f *fakeStateStore) Consume(_ context.Context, state string) (model.StateData, bool, error) {
	if f.err != nil {
		return model.StateData{}, false, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.states[state]
	if !ok {
		return model.StateData{}, false, nil
	}
	delete(f.states, state) // single-use: the defining property
	return data, true, nil
}

type fakeIdentity struct{ userID uuid.UUID }

func (f fakeIdentity) UserID(context.Context) (uuid.UUID, error) { return f.userID, nil }

type fakeTokenStore struct {
	upserts int
	stored  []model.EncryptedToken
}

func (f *fakeTokenStore) Upsert(_ context.Context, tok model.EncryptedToken) error {
	f.upserts++
	f.stored = append(f.stored, tok)
	return nil
}

func (f *fakeTokenStore) ListByUser(context.Context, uuid.UUID) ([]model.Connection, error) {
	return nil, nil
}

func (f *fakeTokenStore) DeleteByUserPlatform(context.Context, uuid.UUID, string) (int64, error) {
	return 0, nil
}

func (f *fakeTokenStore) ListSealed(context.Context, uuid.UUID) ([]model.EncryptedToken, error) {
	return nil, nil
}

type fakeProviderClient struct {
	calls  int
	result ExchangeResult
	err    error
}

func (f *fakeProviderClient) Exchange(context.Context, ExchangeRequest) (ExchangeResult, error) {
	f.calls++
	return f.result, f.err
}

// callbackFixture wires a service whose only real component is the state store,
// which is what these tests are about.
func callbackFixture(t *testing.T, userID uuid.UUID) (*Service, *fakeStateStore, *fakeProviderClient) {
	t.Helper()

	states := newFakeStateStore()
	exchanger := &fakeProviderClient{result: ExchangeResult{AccessToken: "access", ProviderAccountID: "acct"}}

	svc := &Service{
		cfg: Config{RedirectBaseURL: "https://api.example.test"},
		platforms: map[connector.Platform]connector.PlatformConfig{
			connector.PlatformYouTube: {
				Platform: connector.PlatformYouTube,
				Enabled:  true,
				Auth: connector.Auth{
					Type:            connector.AuthOAuth2,
					ClientIDEnv:     "YT_OAUTH_CLIENT_ID",
					ClientSecretEnv: "YT_OAUTH_CLIENT_SECRET",
					Scopes:          []string{"youtube.readonly"},
				},
			},
		},
		identity: fakeIdentity{userID},
		states:   states,
		tokens:   &fakeTokenStore{},
		sealer:   realSealer{newRealCipher(t, 0x01)},
		provider: exchanger,
		secrets:  func(string) string { return "configured" },
	}
	return svc, states, exchanger
}

// The state parameter is the only thing preventing an attacker from completing
// an OAuth flow into a victim's account. Every one of these must be rejected,
// and none may reach the provider's token endpoint.
func TestCallbackRejectsInvalidState(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	other := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	tests := []struct {
		name  string
		seed  func(*fakeStateStore)
		state string
	}{
		{
			name:  "unknown state",
			seed:  func(*fakeStateStore) {},
			state: "never-issued",
		},
		{
			name: "state minted for a different user",
			seed: func(f *fakeStateStore) {
				f.states["s1"] = model.StateData{UserID: other, Provider: ProviderGoogle, Platform: string(connector.PlatformYouTube)}
			},
			state: "s1",
		},
		{
			name: "state minted for a different provider",
			seed: func(f *fakeStateStore) {
				f.states["s2"] = model.StateData{UserID: userID, Provider: ProviderMeta, Platform: string(connector.PlatformInstagram)}
			},
			state: "s2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, states, exchanger := callbackFixture(t, userID)
			tt.seed(states)

			ctx := WithCallbackParams(context.Background(), CallbackParams{Code: "code", State: tt.state})
			_, err := svc.Callback(ctx, ProviderGoogle)

			if err == nil {
				t.Fatal("an invalid state was accepted")
			}
			if got := errs.KindOf(err); got != errs.KindUnauthorized {
				t.Errorf("kind = %v, want KindUnauthorized", got)
			}
			if exchanger.calls != 0 {
				t.Errorf("a rejected callback still called the provider %d time(s)", exchanger.calls)
			}
		})
	}
}

// A state is single-use. Replaying a captured callback URL must fail, even
// though the first use succeeded moments earlier.
func TestCallbackStateIsSingleUse(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	svc, states, exchanger := callbackFixture(t, userID)

	states.states["good"] = model.StateData{
		UserID:   userID,
		Provider: ProviderGoogle,
		Platform: string(connector.PlatformYouTube),
	}

	ctx := WithCallbackParams(context.Background(), CallbackParams{Code: "code", State: "good"})

	if _, err := svc.Callback(ctx, ProviderGoogle); err != nil {
		t.Fatalf("first callback: %v", err)
	}
	if exchanger.calls != 1 {
		t.Fatalf("provider exchange calls = %d, want 1", exchanger.calls)
	}

	// Replay the exact same callback.
	_, err := svc.Callback(ctx, ProviderGoogle)
	if err == nil {
		t.Fatal("a replayed state was accepted")
	}
	if got := errs.KindOf(err); got != errs.KindUnauthorized {
		t.Errorf("replay kind = %v, want KindUnauthorized", got)
	}
	if exchanger.calls != 1 {
		t.Errorf("the replay reached the provider: calls = %d, want 1", exchanger.calls)
	}
}

// A provider that reports a denial must not be treated as a transport failure,
// and its untrusted error text must not be reflected back to the caller.
func TestCallbackDeniedConsentIsInvalidAndNotReflected(t *testing.T) {
	userID := uuid.New()
	svc, _, exchanger := callbackFixture(t, userID)

	const injected = "<script>alert(1)</script>"
	ctx := WithCallbackParams(context.Background(), CallbackParams{
		Error:            "access_denied",
		ErrorDescription: injected,
	})

	_, err := svc.Callback(ctx, ProviderGoogle)
	if err == nil {
		t.Fatal("expected an error when consent was denied")
	}
	if got := errs.KindOf(err); got != errs.KindInvalid {
		t.Errorf("kind = %v, want KindInvalid", got)
	}

	var domain *errs.Error
	if errors.As(err, &domain) && strings.Contains(domain.Message, injected) {
		t.Error("the provider's untrusted error text was reflected to the client")
	}
	if exchanger.calls != 0 {
		t.Error("a denied consent still called the provider")
	}
}

// Missing code or state is a malformed callback, distinct from a forged one.
func TestCallbackRequiresCodeAndState(t *testing.T) {
	userID := uuid.New()

	tests := []struct {
		name   string
		params CallbackParams
	}{
		{"no code", CallbackParams{State: "s"}},
		{"no state", CallbackParams{Code: "c"}},
		{"neither", CallbackParams{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _, exchanger := callbackFixture(t, userID)
			ctx := WithCallbackParams(context.Background(), tt.params)

			_, err := svc.Callback(ctx, ProviderGoogle)
			if got := errs.KindOf(err); got != errs.KindInvalid {
				t.Errorf("kind = %v, want KindInvalid", got)
			}
			if exchanger.calls != 0 {
				t.Error("a malformed callback reached the provider")
			}
		})
	}
}
