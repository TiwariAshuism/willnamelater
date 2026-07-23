package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/authctx"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeRepo is an in-memory Repository. It keeps revoked sessions rather than
// deleting them so that re-presenting a rotated token exercises reuse detection.
type fakeRepo struct {
	users        map[uuid.UUID]model.User
	emails       map[string]uuid.UUID
	sessions     map[uuid.UUID]*model.Session
	byHash       map[string]uuid.UUID
	revokeAllFor []uuid.UUID // user ids passed to RevokeUserSessions, in order

	createUserErr error // when set, CreateUser returns it
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users:    map[uuid.UUID]model.User{},
		emails:   map[string]uuid.UUID{},
		sessions: map[uuid.UUID]*model.Session{},
		byHash:   map[string]uuid.UUID{},
	}
}

func (f *fakeRepo) CreateUser(_ context.Context, in model.NewUser) (model.User, error) {
	if f.createUserErr != nil {
		return model.User{}, f.createUserErr
	}
	if _, ok := f.emails[strings.ToLower(in.Email)]; ok {
		return model.User{}, repository.ErrEmailTaken
	}
	u := model.User{
		ID:           uuid.New(),
		Email:        in.Email,
		PasswordHash: in.PasswordHash,
		FullName:     in.FullName,
		Role:         model.RoleUser,
		Status:       model.StatusActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	f.users[u.ID] = u
	f.emails[strings.ToLower(in.Email)] = u.ID
	return u, nil
}

func (f *fakeRepo) UserByEmail(_ context.Context, email string) (model.User, error) {
	id, ok := f.emails[strings.ToLower(email)]
	if !ok {
		return model.User{}, repository.ErrUserNotFound
	}
	return f.users[id], nil
}

func (f *fakeRepo) UserByID(_ context.Context, id uuid.UUID) (model.User, error) {
	u, ok := f.users[id]
	if !ok {
		return model.User{}, repository.ErrUserNotFound
	}
	return u, nil
}

func (f *fakeRepo) CreateSession(_ context.Context, in model.NewSession) (model.Session, error) {
	s := &model.Session{
		ID:               uuid.New(),
		UserID:           in.UserID,
		RefreshTokenHash: in.RefreshTokenHash,
		UserAgent:        in.UserAgent,
		IP:               in.IP,
		IssuedAt:         time.Now(),
		ExpiresAt:        in.ExpiresAt,
	}
	f.sessions[s.ID] = s
	f.byHash[hex.EncodeToString(in.RefreshTokenHash)] = s.ID
	return *s, nil
}

func (f *fakeRepo) SessionByRefreshHash(_ context.Context, hash []byte) (model.Session, error) {
	id, ok := f.byHash[hex.EncodeToString(hash)]
	if !ok {
		return model.Session{}, repository.ErrSessionNotFound
	}
	return *f.sessions[id], nil
}

func (f *fakeRepo) RevokeSession(_ context.Context, id uuid.UUID) error {
	if s, ok := f.sessions[id]; ok && s.RevokedAt == nil {
		now := time.Now()
		s.RevokedAt = &now
	}
	return nil
}

func (f *fakeRepo) RevokeUserSessions(_ context.Context, userID uuid.UUID) error {
	f.revokeAllFor = append(f.revokeAllFor, userID)
	now := time.Now()
	for _, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeRepo) RotateSession(_ context.Context, oldID uuid.UUID, next model.NewSession) (model.Session, error) {
	old, ok := f.sessions[oldID]
	if !ok || old.RevokedAt != nil {
		return model.Session{}, repository.ErrSessionNotFound
	}
	now := time.Now()
	old.RevokedAt = &now

	s := &model.Session{
		ID:               uuid.New(),
		UserID:           next.UserID,
		RefreshTokenHash: next.RefreshTokenHash,
		UserAgent:        next.UserAgent,
		IP:               next.IP,
		IssuedAt:         now,
		ExpiresAt:        next.ExpiresAt,
	}
	f.sessions[s.ID] = s
	f.byHash[hex.EncodeToString(next.RefreshTokenHash)] = s.ID
	return *s, nil
}

// testJWTConfig returns a config carrying a freshly generated RS256 key.
func testJWTConfig(t *testing.T) config.JWTConfig {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return config.JWTConfig{PrivateKeyPEM: config.Secret(keyPEM)}
}

func newService(t *testing.T, repo repository.Repository) AuthService {
	t.Helper()
	svc, _, err := NewService(repo, testJWTConfig(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// errCode extracts the stable code of a domain error, failing when err is not a
// domain error.
func errCode(t *testing.T, err error) string {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.Error, got %T: %v", err, err)
	}
	return e.Code
}

func TestRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		req      model.RegisterRequest
		seedDup  bool
		wantCode string // empty means success
	}{
		{name: "success", req: model.RegisterRequest{Email: "new@example.com", Password: "supersecret1"}},
		{name: "weak password", req: model.RegisterRequest{Email: "a@example.com", Password: "short"}, wantCode: "auth.weak_password"},
		{name: "invalid email", req: model.RegisterRequest{Email: "not-an-email", Password: "supersecret1"}, wantCode: "auth.invalid_email"},
		{name: "duplicate email", req: model.RegisterRequest{Email: "dupe@example.com", Password: "supersecret1"}, seedDup: true, wantCode: "auth.email_taken"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newFakeRepo()
			if tc.seedDup {
				if _, err := repo.CreateUser(context.Background(), model.NewUser{Email: tc.req.Email, PasswordHash: "x"}); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			svc := newService(t, repo)

			resp, err := svc.Register(context.Background(), tc.req)
			if tc.wantCode != "" {
				if got := errCode(t, err); got != tc.wantCode {
					t.Fatalf("code = %q, want %q", got, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Register: %v", err)
			}
			if resp.AccessToken == "" || resp.RefreshToken == "" {
				t.Fatal("Register: expected non-empty token pair")
			}
			if resp.User.Email != tc.req.Email {
				t.Fatalf("user email = %q, want %q", resp.User.Email, tc.req.Email)
			}
		})
	}
}

func TestLoginDoesNotRevealAccountExistence(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(t, repo)
	const email = "real@example.com"
	const pw = "supersecret1"
	if _, err := svc.Register(context.Background(), model.RegisterRequest{Email: email, Password: pw}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, wrongPassErr := svc.Login(context.Background(), model.LoginRequest{Email: email, Password: "wrongpassword"})
	_, unknownErr := svc.Login(context.Background(), model.LoginRequest{Email: "ghost@example.com", Password: pw})

	if wrongPassErr == nil || unknownErr == nil {
		t.Fatal("both failing logins must return an error")
	}
	if a, b := errCode(t, wrongPassErr), errCode(t, unknownErr); a != b {
		t.Fatalf("error codes differ (%q vs %q); account existence is observable", a, b)
	}
	if got := errCode(t, wrongPassErr); got != "auth.invalid_credentials" {
		t.Fatalf("code = %q, want auth.invalid_credentials", got)
	}
}

func TestLoginSuccessAndSuspended(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(t, repo)
	const pw = "supersecret1"
	reg, err := svc.Register(context.Background(), model.RegisterRequest{Email: "user@example.com", Password: pw})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := svc.Login(context.Background(), model.LoginRequest{Email: "user@example.com", Password: pw}); err != nil {
		t.Fatalf("Login (active): %v", err)
	}

	// Suspend the account and confirm login is refused with the generic error.
	u := repo.users[reg.User.ID]
	u.Status = model.StatusSuspended
	repo.users[u.ID] = u
	if _, err := svc.Login(context.Background(), model.LoginRequest{Email: "user@example.com", Password: pw}); err == nil {
		t.Fatal("suspended account must not log in")
	} else if got := errCode(t, err); got != "auth.invalid_credentials" {
		t.Fatalf("code = %q, want auth.invalid_credentials", got)
	}
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(t, repo)
	reg, err := svc.Register(context.Background(), model.RegisterRequest{Email: "rotate@example.com", Password: "supersecret1"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// First refresh rotates: the old token yields a new pair.
	rotated, err := svc.Refresh(context.Background(), model.RefreshRequest{RefreshToken: reg.RefreshToken})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if rotated.RefreshToken == reg.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}

	// Re-presenting the now-revoked original token is reuse: the whole family is
	// revoked and the request is rejected.
	_, reuseErr := svc.Refresh(context.Background(), model.RefreshRequest{RefreshToken: reg.RefreshToken})
	if reuseErr == nil {
		t.Fatal("reused refresh token must be rejected")
	}
	if got := errCode(t, reuseErr); got != "auth.invalid_refresh_token" {
		t.Fatalf("code = %q, want auth.invalid_refresh_token", got)
	}
	if len(repo.revokeAllFor) != 1 || repo.revokeAllFor[0] != reg.User.ID {
		t.Fatalf("expected session family revoked for %v, got %v", reg.User.ID, repo.revokeAllFor)
	}

	// The rotated token must also now be dead, since the family was revoked.
	if _, err := svc.Refresh(context.Background(), model.RefreshRequest{RefreshToken: rotated.RefreshToken}); err == nil {
		t.Fatal("rotated token must be dead after family revocation")
	}
}

func TestRefreshRejectsInvalidToken(t *testing.T) {
	t.Parallel()

	svc := newService(t, newFakeRepo())
	tests := []struct{ name, token string }{
		{name: "empty", token: ""},
		{name: "not base64url", token: "!!!!"},
		{name: "unknown", token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := svc.Refresh(context.Background(), model.RefreshRequest{RefreshToken: tc.token}); err == nil {
				t.Fatal("expected error")
			} else if got := errCode(t, err); got != "auth.invalid_refresh_token" {
				t.Fatalf("code = %q, want auth.invalid_refresh_token", got)
			}
		})
	}
}

func TestLogoutIsIdempotentAndRevokes(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(t, repo)
	reg, err := svc.Register(context.Background(), model.RegisterRequest{Email: "out@example.com", Password: "supersecret1"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := svc.Logout(context.Background(), model.LogoutRequest{RefreshToken: reg.RefreshToken}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	// The session is now revoked, so the token cannot be refreshed.
	if _, err := svc.Refresh(context.Background(), model.RefreshRequest{RefreshToken: reg.RefreshToken}); err == nil {
		t.Fatal("refresh after logout must fail")
	}
	// Logging out again with an unknown token is a no-op, not an error.
	if err := svc.Logout(context.Background(), model.LogoutRequest{RefreshToken: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}); err != nil {
		t.Fatalf("idempotent Logout: %v", err)
	}
}

func TestMe(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	svc := newService(t, repo)
	reg, err := svc.Register(context.Background(), model.RegisterRequest{Email: "me@example.com", Password: "supersecret1"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Without an identity on the context, Me is unauthenticated.
	if _, err := svc.Me(context.Background()); err == nil {
		t.Fatal("Me without identity must fail")
	} else if got := errCode(t, err); got != "auth.unauthenticated" {
		t.Fatalf("code = %q, want auth.unauthenticated", got)
	}

	ctx := authctx.With(context.Background(), authctx.Identity{UserID: reg.User.ID, Role: model.RoleUser})
	got, err := svc.Me(ctx)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if got.ID != reg.User.ID || got.Email != "me@example.com" {
		t.Fatalf("Me = %+v, want id %v email me@example.com", got, reg.User.ID)
	}
}
