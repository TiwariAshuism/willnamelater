package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/auth/internal/authctx"
	"github.com/getnyx/influaudit/backend/internal/auth/internal/token"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/httpx"
)

// Verifier validates an access token and returns its claims. *token.Issuer
// implements it; declaring it here lets the middleware depend on a narrow
// contract and lets tests substitute a fake.
type Verifier interface {
	Verify(raw string) (token.Claims, error)
}

// bearerPrefix is the scheme expected in the Authorization header.
const bearerPrefix = "Bearer "

// errUnauthenticated is the single response for every authentication failure, so
// a caller cannot distinguish a missing header from a bad signature or an
// expired token.
var errUnauthenticated = errs.New(errs.KindUnauthorized, "auth.unauthenticated", "authentication required")

// RequireAuth authenticates a request from its bearer access token and places
// the caller's identity on the request context for downstream handlers. Any
// failure aborts the chain with a uniform 401.
func RequireAuth(v Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			httpx.RenderError(c, errUnauthenticated)
			return
		}

		claims, err := v.Verify(raw)
		if err != nil {
			httpx.RenderError(c, errUnauthenticated)
			return
		}

		userID, err := uuid.Parse(claims.Subject)
		if err != nil {
			httpx.RenderError(c, errUnauthenticated)
			return
		}

		ctx := authctx.With(c.Request.Context(), authctx.Identity{UserID: userID, Role: claims.Role})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
// The scheme is matched case-insensitively; a missing scheme or empty token
// yields ok=false.
func bearerToken(header string) (string, bool) {
	if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	tok := strings.TrimSpace(header[len(bearerPrefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}
