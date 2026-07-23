// Package api is the apigen source for the auth module: an annotated Go
// interface from which the service and repository interface layers are
// generated. It is the single declaration of the auth HTTP surface.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/model"
)

// AuthAPI is the auth module's HTTP surface. Each method's doc comment is the
// route apigen binds it to. The handler is hand-written (see internal/handler)
// so that errors render through httpx.RenderError rather than apigen's default.
type AuthAPI interface {
	// POST /auth/register
	Register(ctx context.Context, req model.RegisterRequest) (model.AuthResponse, error)
	// POST /auth/login
	Login(ctx context.Context, req model.LoginRequest) (model.AuthResponse, error)
	// POST /auth/refresh
	Refresh(ctx context.Context, req model.RefreshRequest) (model.AuthResponse, error)
	// POST /auth/logout
	Logout(ctx context.Context, req model.LogoutRequest) error
	// GET /auth/me
	Me(ctx context.Context) (model.UserResponse, error)
}
