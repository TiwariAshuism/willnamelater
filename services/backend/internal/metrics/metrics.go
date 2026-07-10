// Package metrics is the public seam of the metrics module: the only surface the
// composition root imports. It wires the module-private handler, service, and
// repository layers together and exposes exactly two capabilities — mounting the
// read routes, and ingesting a connector snapshot — so app never reaches into
// internal/metrics/internal/....
//
// The module owns the metric_point time series, the post table, and the
// pseudonymized comment_sample table. Commenter identities are salted-hash
// pseudonymized on ingest (see the internal/service SaltProvider); that is this
// module's responsibility and happens nowhere else.
package metrics

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/service"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the constructed metrics module. app builds one and mounts it.
type Module struct {
	handler *handler.MetricsHandler
	service *service.Service
}

// New wires the module. pool backs both the read queries and the ingest
// transaction; cipher unseals the application-wide pseudonymization salt and
// must be non-nil, since pseudonymizing commenters is mandatory.
func New(pool *db.Pool, cipher *crypto.Cipher) *Module {
	read := repository.NewMetricsRepository(pool)
	ingest := repository.NewIngestRepository()
	salt := service.NewSaltProvider(repository.NewSaltStore(pool), cipher)
	svc := service.New(pool, read, ingest, salt)

	return &Module{
		handler: handler.New(svc),
		service: svc,
	}
}

// RegisterRoutes mounts the module's read routes on r (typically the /v1 group).
func (m *Module) RegisterRoutes(r gin.IRouter) {
	m.handler.Register(r)
}

// Ingest persists a connector snapshot for one influencer and audit job. The
// audit worker calls this after a fetch; it is not an HTTP route.
func (m *Module) Ingest(ctx context.Context, influencerID, auditJobID uuid.UUID, snap connector.Snapshot) error {
	return m.service.Ingest(ctx, influencerID, auditJobID, snap)
}
