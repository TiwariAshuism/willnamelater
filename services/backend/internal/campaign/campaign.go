// Package campaign is the public entry point of the campaign module: the one
// package outside internal/campaign/internal that the composition root imports.
// It wires the service and handler together and exposes route registration.
//
// The module is a scaffold. Its service returns errs.ErrNotImplemented, so every
// route answers 501 until campaign management is built. The composition root does
// not mount it yet — enabling it is a call to RegisterRoutes on an authenticated
// group, plus the repository work behind the service.
//
// Everything behind this package lives under internal/campaign/internal, which Go
// forbids any sibling module from importing, so a collaborator can only reach
// campaign through this surface.
package campaign

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/campaign/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/campaign/internal/service"
)

// Module is the wired campaign module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New builds the campaign module.
func New() *Module {
	return &Module{handler: handler.New(service.New())}
}

// RegisterRoutes mounts the campaign endpoints on rg (an authenticated group). It
// is not called by the composition root while the module is a scaffold.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}
