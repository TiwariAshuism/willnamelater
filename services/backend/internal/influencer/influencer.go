// Package influencer is the public entry point of the influencer module: the one
// package outside internal/influencer/internal that the composition root
// imports. It wires the repository, service, and handler together and exposes
// route registration.
//
// Everything behind it lives under internal/influencer/internal, which Go
// forbids any sibling module from importing, so a collaborator can only reach
// this module through this surface.
package influencer

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired influencer module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New builds the influencer module over the shared connection pool. It cannot
// fail: every dependency it needs is already constructed.
func New(pool *db.Pool) *Module {
	repo := repository.New(pool)
	return &Module{handler: handler.New(service.New(repo))}
}

// RegisterRoutes mounts the influencer endpoints under rg. Every endpoint here
// identifies or mutates a specific influencer, so the composition root applies
// the auth middleware to the group it passes in.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	m.handler.RegisterRoutes(rg)
}
