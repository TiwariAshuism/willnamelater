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
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/report/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/report/internal/service"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

// Module is the wired report module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New wires the module. Each argument is a consumer-side port (declared in
// internal/report/port) the composition root satisfies with an adapter over the
// audit, scoring, llm, and platform-PDF implementations.
func New(audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, pdf port.PDFRenderer) *Module {
	svc := service.New(audit, score, narrative, pdf)
	return &Module{handler: handler.New(svc)}
}

// RegisterRoutes mounts the report endpoints on rg (typically the protected /v1
// group). Both routes act on behalf of a signed-in caller, so the composition
// root must pass a group carrying the auth middleware.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}
