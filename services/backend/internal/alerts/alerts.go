// Package alerts is the public entry point of the alerts module: the one package
// outside internal/alerts/internal that the composition root imports. It wires the
// service and handler together and exposes route registration.
//
// The module is a scaffold. Its service returns errs.ErrNotImplemented, so every
// route answers 501 until the alerting engine is built. The composition root does
// not mount it yet — enabling it is a call to RegisterRoutes on an authenticated
// group, plus the repository and rule-evaluation work behind the service.
//
// Everything behind this package lives under internal/alerts/internal, which Go
// forbids any sibling module from importing, so a collaborator can only reach
// alerts through this surface.
package alerts

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/alerts/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/alerts/internal/service"
)

// Module is the wired alerts module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New builds the alerts module.
func New() *Module {
	return &Module{handler: handler.New(service.New())}
}

// RegisterRoutes mounts the alerts endpoints on rg (an authenticated group). It
// is not called by the composition root while the module is a scaffold.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}
