// Package waitlist is the public seam of the waitlist module: the one package
// outside internal/waitlist/internal the composition root imports. It wires the
// repository, service, and handler together and exposes route registration.
//
// The module owns one table (email_capture, migration 000034) and one public
// endpoint (POST /waitlist) that captures {email, source} for the funnel's two
// "return later" surfaces — the B3 connect-wall and the F1 media-kit waitlist —
// idempotently on (email, source).
package waitlist

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/service"
)

// Module is the wired waitlist module. Construct it with New and mount its public
// capture route with RegisterPublicRoutes.
type Module struct {
	handler *handler.Handler
}

// New builds the waitlist module over the shared connection pool.
func New(pool *db.Pool) *Module {
	svc := service.New(repository.New(pool))
	return &Module{handler: handler.New(svc)}
}

// RegisterPublicRoutes mounts the unauthenticated capture endpoint (POST
// /waitlist) on the public group.
func (m *Module) RegisterPublicRoutes(rg gin.IRouter) {
	m.handler.RegisterPublicRoutes(rg)
}
