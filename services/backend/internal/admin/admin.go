// Package admin is the public seam of the admin module: the one package outside
// internal/admin/internal that the composition root imports. It wires the
// module-private handler, repository, and service together and exposes route
// registration.
//
// The module owns the dispute table and the dispute-review loop — filing a
// dispute against an audit, the admin review queue, and resolving a dispute with
// a label that services/ml/training later consumes. It also serves two read-only
// operator surfaces: the API-cost dashboard over the llm generation aggregate,
// and the asynq job monitor. It imports no other business module: the llm cost
// reader, the audit fraud reader, the asynq inspector, the caller identity, and
// the admin guard are all reached through ports in internal/admin/port, which
// the composition root satisfies with thin adapters.
package admin

import (
	"github.com/gin-gonic/gin"

	"github.com/getnyx/influaudit/backend/internal/admin/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/admin/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/admin/internal/service"
	"github.com/getnyx/influaudit/backend/internal/admin/port"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired admin module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
}

// New wires the admin module. pool backs the dispute repository; every remaining
// argument is a consumer-side port (declared in internal/admin/port) the
// composition root satisfies with an adapter over the real collaborator, so
// admin never imports auth, llm, audit, or the asynq inspector's construction.
func New(
	pool *db.Pool,
	caller port.CallerID,
	guard port.AdminGuard,
	fraud port.FraudReader,
	cost port.CostReader,
	queues port.QueueInspector,
) *Module {
	svc := service.New(repository.New(pool), caller, guard, fraud, cost, queues)
	return &Module{handler: handler.New(svc)}
}

// RegisterRoutes mounts the admin endpoints on rg (typically the protected /v1
// group). Filing a dispute needs a signed-in caller; every /admin route
// additionally requires the admin role, enforced in the service through the
// AdminGuard port. The composition root must pass a group carrying the auth
// middleware.
func (m *Module) RegisterRoutes(rg gin.IRouter) {
	m.handler.Register(rg)
}
