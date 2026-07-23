package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// FullService is the apigen-generated AuthService (the HTTP surface) PLUS the
// non-route provisioning the composition root needs for OAuth-as-signup. These
// two methods are hand-written because they are not HTTP routes: they are called
// by the oauth module's signup flow through an adapter the composition root wires,
// never over the wire. Widening NewService's first return to this interface lets
// the auth facade reach them without touching the generated AuthService.
type FullService interface {
	AuthService
	ProvisionSocialUser(ctx context.Context, email string) (model.User, error)
	IssueSession(ctx context.Context, userID uuid.UUID, userAgent, ip string) (model.AuthResponse, error)
}

// ProvisionSocialUser finds the user with this email or creates a PASSWORDLESS
// (social-only) account for it. It backs OAuth-as-signup, where identity is
// established by the Meta grant, not a password — password_hash stays NULL, and
// the login path already refuses such an account a password sign-in. It is
// idempotent on email (the case-insensitive unique index), including under a
// concurrent first connect.
func (s *authService) ProvisionSocialUser(ctx context.Context, email string) (model.User, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return model.User{}, err
	}

	user, err := s.repo.UserByEmail(ctx, normalized)
	if err == nil {
		if user.Status != model.StatusActive {
			return model.User{}, errs.New(errs.KindForbidden, "auth.account_not_active", "this account cannot be used")
		}
		return user, nil
	}
	if !errors.Is(err, repository.ErrUserNotFound) {
		return model.User{}, err
	}

	created, cerr := s.repo.CreateUser(ctx, model.NewUser{Email: normalized})
	if cerr != nil {
		if errors.Is(cerr, repository.ErrEmailTaken) {
			// Lost the create race with a concurrent first connect; the winner's row
			// is what everyone reads.
			return s.repo.UserByEmail(ctx, normalized)
		}
		return model.User{}, cerr
	}
	return created, nil
}

// IssueSession mints a fresh token pair and session for an existing user id,
// exactly as the login path does after a successful credential check. It backs
// OAuth-as-signup: once the grant has provisioned the user, this establishes their
// session. It refuses a non-active account.
func (s *authService) IssueSession(ctx context.Context, userID uuid.UUID, userAgent, ip string) (model.AuthResponse, error) {
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return model.AuthResponse{}, err
	}
	if user.Status != model.StatusActive {
		return model.AuthResponse{}, errs.New(errs.KindForbidden, "auth.account_not_active", "this account cannot be used")
	}
	return s.startSession(ctx, user, userAgent, ip)
}
