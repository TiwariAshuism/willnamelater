// Package mlops is the public seam of the mlops module: the one package outside
// internal/mlops/internal that the composition root imports. It wires the
// module-private handler, repository, and service together, exposes route
// registration, and exposes the two in-process operations the composition root
// adapts onto neighbouring modules' ports.
//
// The module owns the champion-challenger retraining pipeline's data surface: the
// feature store (one clean labeled row per completed audit, written through the
// data-quality filter), the model registry (challenger register / promote /
// rollback with an S3 artifact write and a server-side gate-report re-check), the
// canary set, and the shadow prediction log. It imports no other business module:
// the admin guard, the ml service-token auth, and the object store are reached
// through ports in internal/mlops/port, which the composition root satisfies.
//
// Two operations are called in-process rather than over HTTP. RecordFeatureRow is
// adapted onto an audit FeatureRecorder port and called best-effort per completed
// audit; SetFraudLabel is adapted onto an admin TrainingLabelSink port and called
// when a dispute is decided. Both are non-fatal to their callers.
package mlops

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/mlops/contract"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/mlops/internal/service"
	"github.com/getnyx/influaudit/backend/internal/mlops/port"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
)

// FeatureCapture is the input to RecordFeatureRow: everything the module needs to
// compute one frozen feature vector and its data-quality verdict for a completed
// audit. It is re-exported from the module's contract leaf so a port adapter can
// import mlops alone.
type FeatureCapture = contract.FeatureCapture

// FraudSignal is the six-key fraud sub-vector a FeatureCapture carries.
type FraudSignal = contract.FraudSignal

// FraudLabelSource records how a backfilled fraud_label was established.
type FraudLabelSource = contract.FraudLabelSource

// The fraud-label sources, re-exported so an admin-side adapter maps a dispute
// decision onto one without importing the contract leaf.
const (
	LabelSourceDisputeRejected = contract.LabelSourceDisputeRejected
	LabelSourceDisputeUpheld   = contract.LabelSourceDisputeUpheld
)

// Module is the wired mlops module. Construct it with New, mount it with
// RegisterAdminRoutes / RegisterServiceRoutes, and drive its in-process seams
// with RecordFeatureRow / SetFraudLabel.
type Module struct {
	handler *handler.Handler
	svc     *service.Service
}

// New wires the mlops module. pool backs the repository; guard, svcAuth, and
// store are consumer-side ports (declared in internal/mlops/port) the composition
// root satisfies — guard over auth's admin bit, svcAuth over the ml service
// token, and store over the internal/platform/storage client — so mlops imports
// no other business module.
func New(pool *db.Pool, guard port.AdminGuard, svcAuth port.ServiceAuth, store port.ArtifactStore) *Module {
	svc := service.New(repository.New(pool), guard, svcAuth, store)
	return &Module{handler: handler.New(svc), svc: svc}
}

// RegisterAdminRoutes mounts the admin endpoints (feature-row export, model
// register/promote, canaries) under rg. The composition root passes the protected
// /v1 group carrying the admin JWT middleware; the service enforces the admin role
// through its AdminGuard port.
func (m *Module) RegisterAdminRoutes(rg gin.IRouter) {
	m.handler.RegisterAdmin(rg)
}

// RegisterServiceRoutes mounts the prediction-ingest endpoint under rg. The
// composition root passes a /v1 group carrying the ml service-token middleware;
// the service enforces the token through its ServiceAuth port.
func (m *Module) RegisterServiceRoutes(rg gin.IRouter) {
	m.handler.RegisterService(rg)
}

// RecordFeatureRow captures one completed audit as a feature-store row (feature
// vector + data-quality verdict). A capture with no usable snapshot is a no-op.
// The composition root adapts it onto an audit FeatureRecorder port and calls it
// best-effort in the orchestrator's score-and-report step; a returned error must
// be logged and ignored, never allowed to fail the audit.
func (m *Module) RecordFeatureRow(ctx context.Context, capture FeatureCapture) error {
	return m.svc.RecordFeatureRow(ctx, capture)
}

// SetFraudLabel backfills the supervised fraud target on a captured row when a
// dispute is decided. It is a no-op when no row exists for the audit. The
// composition root adapts it onto an admin TrainingLabelSink port and calls it
// after ResolveDispute records a decision; the call is non-fatal to the dispute
// resolution.
func (m *Module) SetFraudLabel(ctx context.Context, auditJobID uuid.UUID, label bool, source FraudLabelSource) error {
	return m.svc.SetFraudLabel(ctx, auditJobID, label, source)
}
