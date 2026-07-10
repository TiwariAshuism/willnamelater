// Package service implements the auth module's business logic: registration,
// login, refresh-token rotation, and logout.
//
// Refresh tokens are opaque and stored only as a SHA-256 hash. Rotation is
// mandatory on every use, and presenting an already-rotated token is treated as
// theft: the entire session family is revoked rather than just that token.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"time"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/authctx"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/password"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/token"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// RefreshTokenTTL bounds how long a refresh token stays valid, and therefore how
// long a returning client can go without re-entering credentials. Thirty days is
// a common balance: long enough that active users are rarely logged out, short
// enough to bound the value of a stolen refresh token.
const RefreshTokenTTL = 30 * 24 * time.Hour

// minPasswordLen is the shortest password accepted at registration. Eight is the
// NIST SP 800-63B floor; the argon2id cost does the heavy lifting beyond it.
const minPasswordLen = 8

// tokenType is the OAuth token_type echoed to clients for the bearer access
// token.
const tokenType = "Bearer"

// refreshTokenBytes is the entropy of an opaque refresh token: 256 bits, far
// beyond guessing range.
const refreshTokenBytes = 32

// Domain errors shared across methods. Login and refresh deliberately reuse a
// single generic error so a caller cannot tell a missing account from a wrong
// password, or a revoked token from an unknown one.
var (
	errInvalidCredentials = errs.New(errs.KindUnauthorized, "auth.invalid_credentials", "invalid email or password")
	errInvalidRefresh     = errs.New(errs.KindUnauthorized, "auth.invalid_refresh_token", "refresh token is invalid or expired")
	errUnauthenticated    = errs.New(errs.KindUnauthorized, "auth.unauthenticated", "authentication required")
	errWeakPassword       = errs.New(errs.KindInvalid, "auth.weak_password", "password must be at least 8 characters")
	errInvalidEmail       = errs.New(errs.KindInvalid, "auth.invalid_email", "a valid email address is required")
)

// authService implements AuthService. It owns the token issuer (whose signing
// key it parses once at construction) and a precomputed dummy hash used to keep
// login timing constant when an email does not exist.
type authService struct {
	repo      repository.Repository
	issuer    *token.Issuer
	now       func() time.Time
	dummyHash string
}

var _ AuthService = (*authService)(nil)

// NewService constructs the auth service. It parses the RS256 signing key from
// jwtCfg up front, so token issuance never pays PEM-parsing cost and a bad key
// fails the process at boot rather than at the first login. The returned Issuer
// is shared with the request-authentication middleware so verification uses the
// same key pair. A dummy password hash is computed once so the login path runs
// an argon2 verification even when the account is absent, closing the timing
// side channel that would otherwise reveal which emails are registered.
func NewService(repo repository.Repository, jwtCfg config.JWTConfig) (AuthService, *token.Issuer, error) {
	issuer, err := token.NewIssuer(jwtCfg)
	if err != nil {
		return nil, nil, err
	}

	dummyHash, err := newDummyHash()
	if err != nil {
		return nil, nil, err
	}

	return &authService{repo: repo, issuer: issuer, now: time.Now, dummyHash: dummyHash}, issuer, nil
}

// Register creates an account and immediately signs the user in, returning a
// fresh token pair.
func (s *authService) Register(ctx context.Context, req model.RegisterRequest) (model.AuthResponse, error) {
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return model.AuthResponse{}, err
	}
	if len(req.Password) < minPasswordLen {
		return model.AuthResponse{}, errWeakPassword
	}

	hash, err := password.Hash(req.Password)
	if err != nil {
		return model.AuthResponse{}, errs.Wrap(err, errs.KindInternal, "auth.hash_failed", "could not process the password")
	}

	user, err := s.repo.CreateUser(ctx, model.NewUser{Email: email, PasswordHash: hash, FullName: req.FullName})
	if err != nil {
		if errors.Is(err, repository.ErrEmailTaken) {
			return model.AuthResponse{}, errs.New(errs.KindConflict, "auth.email_taken", "an account with this email already exists")
		}
		return model.AuthResponse{}, err
	}

	return s.startSession(ctx, user, req.UserAgent, req.IP)
}

// Login verifies credentials and starts a new session. Every failure path
// returns errInvalidCredentials after an argon2 verification, so neither the
// response nor the response time distinguishes an unknown email, a password-less
// (social-only) account, a wrong password, or a non-active account.
func (s *authService) Login(ctx context.Context, req model.LoginRequest) (model.AuthResponse, error) {
	user, err := s.repo.UserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			// Burn equivalent work so timing does not reveal the account is absent.
			_, _ = password.Verify(req.Password, s.dummyHash)
			return model.AuthResponse{}, errInvalidCredentials
		}
		return model.AuthResponse{}, err
	}

	storedHash := user.PasswordHash
	if storedHash == "" {
		storedHash = s.dummyHash
	}

	ok, err := password.Verify(req.Password, storedHash)
	if err != nil {
		return model.AuthResponse{}, errs.Wrap(err, errs.KindInternal, "auth.hash_invalid", "could not verify the password")
	}
	if !ok || user.PasswordHash == "" || user.Status != model.StatusActive {
		return model.AuthResponse{}, errInvalidCredentials
	}

	return s.startSession(ctx, user, req.UserAgent, req.IP)
}

// Refresh rotates a refresh token: the presented token is revoked and a new
// pair is issued. Presenting an already-revoked token is treated as theft — the
// entire session family is revoked and the request is rejected.
func (s *authService) Refresh(ctx context.Context, req model.RefreshRequest) (model.AuthResponse, error) {
	hash, err := hashRefreshToken(req.RefreshToken)
	if err != nil {
		return model.AuthResponse{}, errInvalidRefresh
	}

	session, err := s.repo.SessionByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return model.AuthResponse{}, errInvalidRefresh
		}
		return model.AuthResponse{}, err
	}

	if session.Revoked() {
		// Reuse of a revoked token means the token leaked and both the attacker
		// and the legitimate client hold it. Revoke every session of the user.
		if err := s.repo.RevokeUserSessions(ctx, session.UserID); err != nil {
			return model.AuthResponse{}, err
		}
		return model.AuthResponse{}, errInvalidRefresh
	}
	if session.Expired(s.now()) {
		return model.AuthResponse{}, errInvalidRefresh
	}

	user, err := s.repo.UserByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return model.AuthResponse{}, errInvalidRefresh
		}
		return model.AuthResponse{}, err
	}
	if user.Status != model.StatusActive {
		// The account was disabled after the session was issued; end it.
		if err := s.repo.RevokeSession(ctx, session.ID); err != nil {
			return model.AuthResponse{}, err
		}
		return model.AuthResponse{}, errInvalidRefresh
	}

	rawRefresh, refreshHash, err := newRefreshToken()
	if err != nil {
		return model.AuthResponse{}, errs.Wrap(err, errs.KindInternal, "auth.token_generation_failed", "could not issue a session")
	}

	next := model.NewSession{
		UserID:           user.ID,
		RefreshTokenHash: refreshHash,
		UserAgent:        req.UserAgent,
		IP:               req.IP,
		ExpiresAt:        s.now().Add(RefreshTokenTTL),
	}
	if _, err := s.repo.RotateSession(ctx, session.ID, next); err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			// Lost the rotation race: the row was revoked concurrently, which is
			// the same reuse signal. Revoke the family and reject.
			if revErr := s.repo.RevokeUserSessions(ctx, session.UserID); revErr != nil {
				return model.AuthResponse{}, revErr
			}
			return model.AuthResponse{}, errInvalidRefresh
		}
		return model.AuthResponse{}, err
	}

	return s.authResponse(user, rawRefresh)
}

// Logout revokes the session identified by the presented refresh token. It is
// idempotent and never reveals whether the token matched a session, so it cannot
// be used to probe for valid tokens.
func (s *authService) Logout(ctx context.Context, req model.LogoutRequest) error {
	hash, err := hashRefreshToken(req.RefreshToken)
	if err != nil {
		return errInvalidRefresh
	}

	session, err := s.repo.SessionByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, repository.ErrSessionNotFound) {
			return nil
		}
		return err
	}

	return s.repo.RevokeSession(ctx, session.ID)
}

// Me returns the authenticated caller's profile. The identity is established by
// the auth middleware and read from the context; its absence is an internal
// wiring fault surfaced as unauthenticated rather than a leak.
func (s *authService) Me(ctx context.Context) (model.UserResponse, error) {
	id, ok := authctx.From(ctx)
	if !ok {
		return model.UserResponse{}, errUnauthenticated
	}

	user, err := s.repo.UserByID(ctx, id.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return model.UserResponse{}, errUnauthenticated
		}
		return model.UserResponse{}, err
	}

	return toUserResponse(user), nil
}

// startSession issues a token pair and persists a new session for user.
func (s *authService) startSession(ctx context.Context, user model.User, userAgent, ip string) (model.AuthResponse, error) {
	rawRefresh, refreshHash, err := newRefreshToken()
	if err != nil {
		return model.AuthResponse{}, errs.Wrap(err, errs.KindInternal, "auth.token_generation_failed", "could not issue a session")
	}

	_, err = s.repo.CreateSession(ctx, model.NewSession{
		UserID:           user.ID,
		RefreshTokenHash: refreshHash,
		UserAgent:        userAgent,
		IP:               ip,
		ExpiresAt:        s.now().Add(RefreshTokenTTL),
	})
	if err != nil {
		return model.AuthResponse{}, err
	}

	return s.authResponse(user, rawRefresh)
}

// authResponse mints an access token and assembles the response returned by
// register, login, and refresh.
func (s *authService) authResponse(user model.User, rawRefresh string) (model.AuthResponse, error) {
	issued, err := s.issuer.Issue(user.ID, user.Role)
	if err != nil {
		return model.AuthResponse{}, errs.Wrap(err, errs.KindInternal, "auth.token_generation_failed", "could not issue a session")
	}

	return model.AuthResponse{
		AccessToken:  issued.Token,
		RefreshToken: rawRefresh,
		TokenType:    tokenType,
		ExpiresIn:    int64(token.AccessTokenTTL / time.Second),
		User:         toUserResponse(user),
	}, nil
}

// toUserResponse projects a user onto its client-safe shape.
func toUserResponse(u model.User) model.UserResponse {
	return model.UserResponse{
		ID:            u.ID,
		Email:         u.Email,
		FullName:      u.FullName,
		Role:          u.Role,
		EmailVerified: u.EmailVerified,
		CreatedAt:     u.CreatedAt,
	}
}

// normalizeEmail validates and canonicalizes an email address.
func normalizeEmail(email string) (string, error) {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", errInvalidEmail
	}
	return addr.Address, nil
}

// newRefreshToken returns a fresh opaque refresh token as a base64url string and
// the SHA-256 hash to store for it. The raw token is never persisted.
func newRefreshToken() (raw string, hash []byte, err error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(b), sum[:], nil
}

// hashRefreshToken decodes a presented refresh token and returns its stored
// hash. A malformed token or wrong length is rejected before any lookup.
func hashRefreshToken(raw string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != refreshTokenBytes {
		return nil, errInvalidRefresh
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

// newDummyHash produces an argon2id hash of a random secret. It is never a valid
// credential; its only purpose is to give the "unknown email" login path the
// same argon2 cost as a real verification.
func newDummyHash() (string, error) {
	secret := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return password.Hash(base64.RawURLEncoding.EncodeToString(secret))
}
