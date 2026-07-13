// Package report is the public seam of the report module: the one package
// outside internal/report/internal that the composition root imports. It wires
// the module-private service and handler together and exposes route
// registration.
//
// The report module owns no table. It assembles a finished audit's deliverable —
// a JSON view and an on-demand PDF — from data owned by the audit, scoring, and
// llm modules, reached only through the ports in internal/report/port. The
// composition root builds a thin adapter from each real module onto those ports,
// so this module imports no other business module.
package report

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/report/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/report/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/report/internal/service"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

// Module is the wired report module. Construct it with New, mount its
// caller-scoped routes with RegisterRoutes, and its public badge route with
// RegisterPublicRoutes.
type Module struct {
	svc     *service.Service
	handler *handler.Handler
}

// New wires the module. pool backs the report table (the durable published-badge
// record); every remaining argument is a consumer-side port (declared in
// internal/report/port) the composition root satisfies with an adapter over the
// audit, scoring, llm, influencer, platform-PDF, and object-storage
// implementations. caller and owner back the creator-ownership gate: only the
// creator who connected an account may publish or share a report built from it.
func New(pool *db.Pool, audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, fraud port.FraudReader, pdf port.PDFRenderer, storage port.Storage, caller port.CallerID, owner port.OwnerReader, mailer port.Mailer, recipients port.Recipient) *Module {
	svc := service.New(audit, score, narrative, fraud, pdf, repository.New(pool), storage, caller, owner, mailer, recipients)
	return &Module{svc: svc, handler: handler.New(svc)}
}

// RevokeAllForUser withdraws every report and share grant belonging to a user.
// The composition root calls it from the Meta deauthorize / data-deletion
// callbacks and from an explicit user deletion request: once a creator
// disconnects, nothing built from their Instagram data stays reachable (Meta
// Platform Terms §3.d). It returns the number of share grants withdrawn.
func (m *Module) RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	return m.svc.RevokeAllForUser(ctx, userID)
}

// RegisterRoutes mounts the caller-scoped report endpoints on rg (typically the
// protected /v1 group). Every route acts on behalf of a signed-in caller, so the
// composition root must pass a group carrying the auth middleware.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}

// RegisterPublicRoutes mounts the unauthenticated public badge route on rg
// (typically the public /v1 group, with no auth middleware). A badge is a
// shareable public credential; it exposes only the frozen snapshot captured at
// publish time.
func (m *Module) RegisterPublicRoutes(rg gin.IRouter) {
	m.handler.RegisterPublic(rg)
}
