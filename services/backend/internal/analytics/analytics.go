// Package analytics is the public seam of the analytics module: the one package
// outside internal/analytics/internal the composition root imports. It wires the
// repository, service, and handler together and exposes route registration plus
// the share-open recorder other modules feed through a consumer-side port.
//
// The module owns one append-only table (analytics_event, migration 000033) and
// records two kinds of signal: first-party funnel events posted by the browser to
// the public POST /events endpoint, and server-side share opens recorded by the
// report module when a public badge/handle page is read.
package analytics

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/analytics/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/analytics/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/analytics/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired analytics module. Construct it with New, mount its public
// ingest with RegisterPublicRoutes and its optional summary with
// RegisterProtectedRoutes.
type Module struct {
	handler *handler.Handler
	svc     *service.Service
}

// New builds the analytics module over the shared connection pool.
func New(pool *db.Pool) *Module {
	svc := service.New(repository.New(pool))
	return &Module{handler: handler.New(svc), svc: svc}
}

// RegisterPublicRoutes mounts the unauthenticated first-party ingest endpoint
// (POST /events) on the public group.
func (m *Module) RegisterPublicRoutes(rg gin.IRouter) {
	m.handler.RegisterPublicRoutes(rg)
}

// RegisterProtectedRoutes mounts the optional aggregate read (GET /events/summary)
// on the protected group the composition root guards with the auth middleware.
func (m *Module) RegisterProtectedRoutes(rg gin.IRouter) {
	m.handler.RegisterProtectedRoutes(rg)
}

// RecordShareOpen records that a public badge/handle page was opened, attributing
// it to the owner or an external visitor. It is the implementation the composition
// root adapts onto the report module's OpenRecorder port (RecordOpen(ctx, slug,
// owner)): the report module — the reader of every public badge — calls its port
// on each read, and the orchestrator's one-line adapter forwards to this method,
// so report never imports analytics.
func (m *Module) RecordShareOpen(ctx context.Context, publicSlug string, isOwner bool) error {
	return m.svc.RecordShareOpen(ctx, publicSlug, isOwner)
}
