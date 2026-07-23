// Package whitelabel is the public entry point of the whitelabel module: the one
// package outside internal/whitelabel/internal that the composition root imports.
// It wires the service and handler together and exposes route registration.
//
// The module is a scaffold. Its service returns errs.ErrNotImplemented, so every
// route answers 501 until white-label branding is built. The composition root
// does not mount it yet — enabling it is a call to RegisterRoutes on an
// authenticated group, plus the repository work behind the service.
//
// Everything behind this package lives under internal/whitelabel/internal, which
// Go forbids any sibling module from importing, so a collaborator can only reach
// whitelabel through this surface.
package whitelabel

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/whitelabel/internal/service"
)

// Module is the wired whitelabel module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New builds the whitelabel module.
func New() *Module {
	return &Module{handler: handler.New(service.New())}
}

// RegisterRoutes mounts the whitelabel endpoints on rg (an authenticated group).
// It is not called by the composition root while the module is a scaffold.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}
