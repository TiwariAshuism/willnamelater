// Package dataimport is the public seam of the dataimport module: the one
// package outside internal/dataimport/internal that the composition root
// imports. It wires the repository, service, and handler and exposes route
// registration.
//
// The module is the real-data ingress for a platform with no live API grant yet:
// a creator uploads their own Instagram Insights export and it is normalized and
// stored, to be served back by the csvimport connector at audit time. It imports
// no other business module; the authenticated caller is reached through the
// Identity port the composition root satisfies.
package dataimport

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/service"
	"github.com/getnyx/influaudit/backend/internal/dataimport/port"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired dataimport module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New wires the module over the shared pool. identity resolves the authenticated
// caller; the composition root adapts auth's context accessor onto it.
func New(pool *db.Pool, identity port.Identity) *Module {
	svc := service.New(repository.New(pool), identity)
	return &Module{handler: handler.New(svc)}
}

// RegisterRoutes mounts the import endpoints on rg (typically the protected /v1
// group). Every route acts on behalf of a signed-in caller, so the composition
// root must pass a group carrying the auth middleware.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}
