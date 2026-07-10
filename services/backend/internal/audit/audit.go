// Package audit is the public seam of the audit orchestrator: the one package
// outside internal/audit/internal that the composition root imports. It wires
// the module-private handler, repository, and service together and exposes three
// capabilities — mounting the HTTP routes, registering the audit:run worker
// task, and the ports app must satisfy to wire the real collaborators.
//
// The orchestrator owns the audit_job and audit_platform_result tables and the
// fetch/score/report pipeline behind them. It imports no other business module:
// every collaborator is reached through a port declared in
// internal/audit/port, and the composition root builds a thin adapter from each
// real implementation onto the matching port.
package audit

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/audit/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/audit/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/audit/internal/service"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// Module is the wired audit orchestrator. Construct it with New, mount its
// routes with RegisterRoutes, and register its worker task with RegisterTasks.
type Module struct {
	handler *handler.Handler
	svc     *service.Service
}

// New wires the audit module. pool backs the repository; asynqClient enqueues
// the run task; every remaining argument is a collaborator port (declared in
// internal/audit/port) the composition root satisfies with an adapter over the
// real module, so audit never imports billing, metrics, scoring, ml, llm,
// oauth, or auth.
func New(
	pool *db.Pool,
	asynqClient *asynq.Client,
	quota port.Quota,
	ingest port.Ingester,
	scorer port.Scorer,
	fraud port.FraudClient,
	reporter port.Reporter,
	connectors port.Connectors,
	connections port.Connections,
	caller port.CallerID,
) *Module {
	svc := service.New(
		repository.New(pool),
		asynqClient,
		quota,
		ingest,
		scorer,
		fraud,
		reporter,
		connectors,
		connections,
		caller,
	)

	return &Module{
		handler: handler.New(svc),
		svc:     svc,
	}
}

// RegisterRoutes mounts the audit endpoints on rg (typically the /v1 group).
// Every route acts on behalf of a signed-in caller, so the composition root must
// pass a group carrying the auth middleware.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	m.handler.Register(rg)
}

// RegisterTasks registers the audit:run worker handler on mux. It is the
// task-side counterpart of RegisterRoutes: the worker's composition root calls
// it so the module owns which task type it consumes.
func (m *Module) RegisterTasks(mux *asynq.ServeMux) {
	mux.HandleFunc(service.TaskAuditRun, m.svc.ProcessRun)
}

// View is the audit job in the shape the report module needs. It is returned
// already scoped to the authenticated caller.
type View struct {
	ID           uuid.UUID
	InfluencerID uuid.UUID
	Status       string
	// Platforms names the platforms the audit attempted or collected, for the
	// report header.
	Platforms   []string
	RequestedAt time.Time
	FinishedAt  *time.Time
}

// AuditView returns one of the caller's audits for the report module to render.
// It reuses the caller-scoped read, so a caller can never obtain a view of an
// audit that is not theirs; the composition root adapts it onto the report
// module's AuditReader port. It is not an HTTP route.
func (m *Module) AuditView(ctx context.Context, auditID string) (View, error) {
	resp, err := m.svc.GetAudit(ctx, auditID)
	if err != nil {
		return View{}, err
	}

	view := View{
		Status:      resp.Status,
		Platforms:   make([]string, 0, len(resp.Platforms)),
		RequestedAt: resp.RequestedAt,
		FinishedAt:  resp.FinishedAt,
	}
	// GetAudit validated and loaded the job, so these ids parse; ignore the error
	// and leave uuid.Nil if a field was ever empty (an influencer removed after
	// the run, in which case the report has no score to show).
	view.ID, _ = uuid.Parse(resp.ID)
	view.InfluencerID, _ = uuid.Parse(resp.InfluencerID)
	for _, p := range resp.Platforms {
		view.Platforms = append(view.Platforms, p.Platform)
	}
	return view, nil
}
