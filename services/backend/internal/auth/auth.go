// Package auth is the public entry point of the auth module: the one package
// outside internal/auth/internal that the composition root imports. It wires the
// repository, service, and handler together and exposes route registration and
// the request-authentication middleware other modules' protected routes reuse.
//
// Everything behind it lives under internal/auth/internal, which Go forbids any
// sibling module from importing, so a collaborator can only reach auth through
// this surface.
package auth

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/authctx"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/config"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired auth module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler    *handler.Handler
	middleware gin.HandlerFunc
}

// New builds the auth module over the shared connection pool, parsing the RS256
// signing key from jwtCfg once (inside the service constructor). It fails when
// the key is missing or malformed so a misconfigured deployment cannot start.
func New(pool *db.Pool, jwtCfg config.JWTConfig) (*Module, error) {
	svc, issuer, err := service.NewService(repository.New(pool), jwtCfg)
	if err != nil {
		return nil, err
	}

	return &Module{
		handler:    handler.New(svc),
		middleware: handler.RequireAuth(issuer),
	}, nil
}

// RegisterRoutes mounts the auth endpoints under rg (typically the /v1 group).
// Every endpoint is public except /auth/me, which requires a valid access token.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	g := rg.Group("/auth")
	g.POST("/register", m.handler.Register)
	g.POST("/login", m.handler.Login)
	g.POST("/refresh", m.handler.Refresh)
	g.POST("/logout", m.handler.Logout)
	g.GET("/me", m.middleware, m.handler.Me)
}

// Middleware returns the request-authentication middleware. The composition root
// applies it to other modules' protected route groups, so token verification
// lives in exactly one place.
func (m *Module) Middleware() gin.HandlerFunc {
	return m.middleware
}

// UserID returns the authenticated caller's id from ctx, and false when the
// request was not authenticated.
//
// It exists because the identity is stored under internal/auth/internal/authctx,
// which Go forbids any other package from importing. That is deliberate: no
// package outside this module can forge an identity by writing the same context
// value. The composition root reads the caller through this accessor and adapts
// it to whatever port a collaborating module declares.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := authctx.From(ctx)
	return id.UserID, ok
}
