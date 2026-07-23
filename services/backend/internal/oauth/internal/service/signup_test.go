package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/oauth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeUserProvisioner records the email it was asked to provision and hands back
// a fixed id, standing in for the auth module the composition root adapts.
type fakeUserProvisioner struct {
	seenEmail string
	calls     int
	userID    uuid.UUID
	err       error
}

func (f *fakeUserProvisioner) FindOrCreateUserByEmail(_ context.Context, email string) (uuid.UUID, error) {
	f.calls++
	f.seenEmail = email
	if f.err != nil {
		return uuid.Nil, f.err
	}
	return f.userID, nil
}

// fakeInfluencerProvisioner records the upsert input so a test can assert the
// account id and handle resolved off the Meta identity were threaded through.
type fakeInfluencerProvisioner struct {
	seen         InfluencerSignup
	calls        int
	influencerID uuid.UUID
	err          error
}

func (f *fakeInfluencerProvisioner) UpsertInstagramInfluencer(_ context.Context, in InfluencerSignup) (uuid.UUID, error) {
	f.calls++
	f.seen = in
	if f.err != nil {
		return uuid.Nil, f.err
	}
	return f.influencerID, nil
}

// fakeSessionIssuer records the user it minted a session for and returns a fixed
// token bundle.
type fakeSessionIssuer struct {
	seenUser uuid.UUID
	calls    int
	session  model.AuthSession
	err      error
}

func (f *fakeSessionIssuer) IssueSession(_ context.Context, userID uuid.UUID) (model.AuthSession, error) {
	f.calls++
	f.seenUser = userID
	if f.err != nil {
		return model.AuthSession{}, f.err
	}
	s := f.session
	s.UserID = userID
	return s, nil
}

// signupProviderStub captures the ExchangeRequest so a test can assert the PKCE
// verifier from the consumed state reached the exchange, and returns a
// configurable result or error.
type signupProviderStub struct {
	mu     sync.Mutex
	req    ExchangeRequest
	calls  int
	result ExchangeResult
	err    error
}

func (f *signupProviderStub) Exchange(_ context.Context, req ExchangeRequest) (ExchangeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.req = req
	return f.result, f.err
}

type signupDeps struct {
	svc         *Service
	states      *fakeStateStore
	provider    *signupProviderStub
	tokens      *fakeTokenStore
	users       *fakeUserProvisioner
	influencers *fakeInfluencerProvisioner
	sessions    *fakeSessionIssuer
}

// signupFixture wires a Service with Instagram enabled and every signup
// collaborator faked. The sealer is the real envelope cipher so the
// token-never-leaks assertions exercise real cryptography.
func signupFixture(t *testing.T) signupDeps {
	t.Helper()

	states := newFakeStateStore()
	prov := &signupProviderStub{result: ExchangeResult{
		AccessToken:           "ig-access-token",
		ProviderAccountID:     "17841400000000001",
		ProviderAccountHandle: "creator.handle",
		ProviderUserID:        "meta-user-1",
	}}
	tokens := &fakeTokenStore{}
	users := &fakeUserProvisioner{userID: uuid.MustParse("11111111-1111-1111-1111-111111111111")}
	influencers := &fakeInfluencerProvisioner{influencerID: uuid.MustParse("22222222-2222-2222-2222-222222222222")}
	sessions := &fakeSessionIssuer{session: model.AuthSession{
		AccessToken:  "session-access",
		RefreshToken: "session-refresh",
		TokenType:    "Bearer",
		ExpiresIn:    900,
		Email:        "creator@example.test",
	}}

	svc := &Service{
		cfg: Config{RedirectBaseURL: "https://api.example.test"},
		platforms: map[connector.Platform]connector.PlatformConfig{
			connector.PlatformInstagram: {
				Platform: connector.PlatformInstagram,
				Enabled:  true,
				Auth: connector.Auth{
					Type:            connector.AuthOAuth2,
					ClientIDEnv:     "META_OAUTH_CLIENT_ID",
					ClientSecretEnv: "META_OAUTH_CLIENT_SECRET",
					Scopes:          []string{"instagram_basic", "pages_show_list"},
				},
			},
		},
		identity:    fakeIdentity{uuid.Nil},
		states:      states,
		tokens:      tokens,
		sealer:      realSealer{newRealCipher(t, 0x01)},
		provider:    prov,
		secrets:     func(string) string { return "configured" },
		users:       users,
		influencers: influencers,
		sessions:    sessions,
	}
	return signupDeps{svc, states, prov, tokens, users, influencers, sessions}
}

// seedSignupState writes a valid signup state and returns its key. The verifier
// is a known value so the exchange assertion can prove PKCE was threaded.
func seedSignupState(d signupDeps, email, verifier string) string {
	const state = "signup-state"
	d.states.states[state] = model.StateData{
		Provider:     ProviderMeta,
		Platform:     string(connector.PlatformInstagram),
		CodeVerifier: verifier,
		Signup:       true,
		Email:        email,
	}
	return state
}

func TestAuthorizeSignupRejectsInvalidEmail(t *testing.T) {
	for _, email := range []string{"", "   ", "not-an-email", "a@b@c", "no@domain@"} {
		d := signupFixture(t)
		_, err := d.svc.AuthorizeSignup(context.Background(), email)
		if err == nil {
			t.Fatalf("email %q was accepted", email)
		}
		if got := errs.KindOf(err); got != errs.KindInvalid {
			t.Errorf("email %q: kind = %v, want KindInvalid", email, got)
		}
		if len(d.states.states) != 0 {
			t.Errorf("email %q: a state was minted for an invalid email", email)
		}
	}
}

func TestAuthorizeSignupHappyPath(t *testing.T) {
	d := signupFixture(t)

	resp, err := d.svc.AuthorizeSignup(context.Background(), "  Creator@Example.test  ")
	if err != nil {
		t.Fatalf("AuthorizeSignup: %v", err)
	}
	if resp.State == "" {
		t.Fatal("no state returned")
	}
	if !strings.Contains(resp.AuthorizationURL, "facebook.com") {
		t.Errorf("authorization url is not a Meta consent url: %q", resp.AuthorizationURL)
	}
	// The redirect must point at the DISTINCT signup callback path.
	if !strings.Contains(resp.AuthorizationURL, "signup") {
		t.Errorf("authorization url does not use the signup callback: %q", resp.AuthorizationURL)
	}

	saved, ok := d.states.states[resp.State]
	if !ok {
		t.Fatal("state was not persisted")
	}
	if !saved.Signup {
		t.Error("persisted state is not marked as a signup")
	}
	if saved.Email != "Creator@Example.test" {
		t.Errorf("persisted email = %q, want the trimmed address", saved.Email)
	}
	if saved.UserID != uuid.Nil {
		t.Errorf("signup state carries a user id %v, want nil", saved.UserID)
	}
	if saved.CodeVerifier == "" {
		t.Error("no PKCE verifier persisted")
	}
}

func TestCallbackSignupHappyPath(t *testing.T) {
	d := signupFixture(t)
	const verifier = "known-verifier-value"
	state := seedSignupState(d, "creator@example.test", verifier)

	session, err := d.svc.CallbackSignup(context.Background(), CallbackParams{Code: "code", State: state})
	if err != nil {
		t.Fatalf("CallbackSignup: %v", err)
	}

	// User provisioned from the captured email.
	if d.users.calls != 1 || d.users.seenEmail != "creator@example.test" {
		t.Errorf("user provisioning: calls=%d email=%q", d.users.calls, d.users.seenEmail)
	}
	// Influencer upserted for the connected account, owned by the new user.
	if d.influencers.calls != 1 {
		t.Fatalf("influencer upsert calls = %d, want 1", d.influencers.calls)
	}
	if d.influencers.seen.OwnerUserID != d.users.userID {
		t.Errorf("influencer owner = %v, want the provisioned user %v", d.influencers.seen.OwnerUserID, d.users.userID)
	}
	if d.influencers.seen.InstagramAccountID != "17841400000000001" || d.influencers.seen.Handle != "creator.handle" {
		t.Errorf("influencer account/handle not threaded: %+v", d.influencers.seen)
	}
	if d.influencers.seen.ProviderUserID != "meta-user-1" {
		t.Errorf("influencer provider user id = %q", d.influencers.seen.ProviderUserID)
	}
	// Connection persisted, bound to the new user.
	if d.tokens.upserts != 1 {
		t.Fatalf("token upserts = %d, want 1", d.tokens.upserts)
	}
	if got := d.tokens.stored[0].UserID; got != d.users.userID {
		t.Errorf("persisted token owner = %v, want %v", got, d.users.userID)
	}
	if got := d.tokens.stored[0].ProviderAccountID; got != "17841400000000001" {
		t.Errorf("persisted token account = %q", got)
	}
	// Session minted for the new user and returned.
	if d.sessions.calls != 1 || d.sessions.seenUser != d.users.userID {
		t.Errorf("session issue: calls=%d user=%v", d.sessions.calls, d.sessions.seenUser)
	}
	if session.AccessToken != "session-access" || session.RefreshToken != "session-refresh" {
		t.Errorf("session tokens not returned: %+v", session)
	}
	if session.UserID != d.users.userID {
		t.Errorf("session user = %v, want %v", session.UserID, d.users.userID)
	}
	// PKCE verifier from the consumed state reached the exchange.
	if d.provider.req.CodeVerifier != verifier {
		t.Errorf("exchange verifier = %q, want %q", d.provider.req.CodeVerifier, verifier)
	}
	// The exchange used the distinct signup redirect uri.
	if !strings.Contains(d.provider.req.RedirectURI, "/oauth/meta/signup/callback") {
		t.Errorf("exchange redirect uri = %q", d.provider.req.RedirectURI)
	}
}

func TestCallbackSignupMissingIGBusinessAccountIsGuidedFix(t *testing.T) {
	d := signupFixture(t)
	// The provider surfaces the sentinel a login with no linked IG account yields.
	d.provider.result = ExchangeResult{}
	d.provider.err = ErrNoInstagramBusinessAccount
	state := seedSignupState(d, "creator@example.test", "v")

	_, err := d.svc.CallbackSignup(context.Background(), CallbackParams{Code: "code", State: state})
	if err == nil {
		t.Fatal("a missing IG business account was accepted")
	}
	if got := errs.KindOf(err); got != errs.KindInvalid {
		t.Errorf("kind = %v, want KindInvalid", got)
	}
	var domain *errs.Error
	if !errors.As(err, &domain) || domain.Code != "oauth.instagram_business_account_required" {
		t.Errorf("code = %v, want oauth.instagram_business_account_required", err)
	}
	// Nothing was provisioned on the guided-fix path.
	if d.users.calls != 0 || d.influencers.calls != 0 || d.tokens.upserts != 0 || d.sessions.calls != 0 {
		t.Errorf("a rejected signup provisioned something: users=%d infl=%d tokens=%d sessions=%d",
			d.users.calls, d.influencers.calls, d.tokens.upserts, d.sessions.calls)
	}
}

func TestCallbackSignupRejectsInvalidState(t *testing.T) {
	tests := []struct {
		name string
		seed func(signupDeps) string
	}{
		{
			name: "unknown state",
			seed: func(signupDeps) string { return "never-issued" },
		},
		{
			name: "a connect-flow state replayed against signup",
			seed: func(d signupDeps) string {
				const s = "connect-state"
				d.states.states[s] = model.StateData{
					UserID:   uuid.New(),
					Provider: ProviderMeta,
					Platform: string(connector.PlatformInstagram),
					Signup:   false, // NOT a signup state
				}
				return s
			},
		},
		{
			name: "a signup state for a different provider",
			seed: func(d signupDeps) string {
				const s = "wrong-provider"
				d.states.states[s] = model.StateData{Provider: ProviderGoogle, Signup: true}
				return s
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := signupFixture(t)
			state := tt.seed(d)

			_, err := d.svc.CallbackSignup(context.Background(), CallbackParams{Code: "code", State: state})
			if err == nil {
				t.Fatal("an invalid state was accepted")
			}
			if got := errs.KindOf(err); got != errs.KindUnauthorized {
				t.Errorf("kind = %v, want KindUnauthorized", got)
			}
			if d.provider.calls != 0 {
				t.Errorf("a rejected callback reached the provider %d time(s)", d.provider.calls)
			}
			if d.users.calls != 0 || d.tokens.upserts != 0 {
				t.Error("a rejected callback provisioned something")
			}
		})
	}
}

func TestCallbackSignupStateIsSingleUse(t *testing.T) {
	d := signupFixture(t)
	state := seedSignupState(d, "creator@example.test", "v")

	if _, err := d.svc.CallbackSignup(context.Background(), CallbackParams{Code: "code", State: state}); err != nil {
		t.Fatalf("first callback: %v", err)
	}
	if d.provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", d.provider.calls)
	}

	// Replay the same state.
	_, err := d.svc.CallbackSignup(context.Background(), CallbackParams{Code: "code", State: state})
	if err == nil {
		t.Fatal("a replayed signup state was accepted")
	}
	if got := errs.KindOf(err); got != errs.KindUnauthorized {
		t.Errorf("replay kind = %v, want KindUnauthorized", got)
	}
	if d.provider.calls != 1 {
		t.Errorf("the replay reached the provider: calls = %d, want 1", d.provider.calls)
	}
}

func TestCallbackSignupMissingCodeOrState(t *testing.T) {
	for _, tt := range []struct {
		name   string
		params CallbackParams
	}{
		{"no code", CallbackParams{State: "s"}},
		{"no state", CallbackParams{Code: "c"}},
		{"denied consent", CallbackParams{Error: "access_denied", ErrorDescription: "<script>x</script>"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := signupFixture(t)
			_, err := d.svc.CallbackSignup(context.Background(), tt.params)
			if got := errs.KindOf(err); got != errs.KindInvalid {
				t.Errorf("kind = %v, want KindInvalid", got)
			}
			if d.provider.calls != 0 {
				t.Error("a malformed callback reached the provider")
			}
			// Untrusted provider text must never be reflected.
			var domain *errs.Error
			if errors.As(err, &domain) && strings.Contains(domain.Message, "script") {
				t.Error("provider error text was reflected")
			}
		})
	}
}

// The whole point of the module: what leaves it — the persisted row AND the
// returned session — must never contain the decrypted platform access token, and
// the plaintext token must never be handed to the user/influencer collaborators.
func TestCallbackSignupNeverLeaksDecryptedToken(t *testing.T) {
	d := signupFixture(t)
	const secretToken = "ig-access-token" // matches the fixture's exchange result
	state := seedSignupState(d, "creator@example.test", "v")

	session, err := d.svc.CallbackSignup(context.Background(), CallbackParams{Code: "code", State: state})
	if err != nil {
		t.Fatalf("CallbackSignup: %v", err)
	}

	// The returned session carries only auth tokens, never the platform token.
	for name, v := range map[string]string{
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
	} {
		if strings.Contains(v, secretToken) {
			t.Errorf("session %s leaked the decrypted platform token", name)
		}
	}

	// Every persisted column that maps to the database is ciphertext.
	stored := d.tokens.stored[0]
	for name, blob := range map[string][]byte{
		"access_token_enc": stored.AccessTokenEnc,
		"dek_wrapped":      stored.DEKWrapped,
	} {
		if len(blob) == 0 {
			t.Errorf("%s is empty; nothing was sealed", name)
		}
		if bytes.Contains(blob, []byte(secretToken)) {
			t.Errorf("%s contains the plaintext access token", name)
		}
	}

	// The collaborators only ever saw ids/email/handle, never the token.
	if strings.Contains(d.influencers.seen.Handle+d.influencers.seen.InstagramAccountID, secretToken) {
		t.Error("the influencer provisioner was handed the plaintext token")
	}
}
