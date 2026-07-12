// Package api is the apigen source for the mlops module: an annotated Go
// interface from which the service interface is generated (apigen -layers
// service). The handler, repository, and service implementation are hand-written.
//
// The mlops module owns the champion-challenger retraining pipeline's data
// surface: the feature store, the model registry, the canary set, and the shadow
// prediction log. The admin routes (feature-row export, model register/promote,
// canaries) are gated by the module's AdminGuard port — the trainer authenticates
// with the same admin JWT `make ml-train` passes. The prediction-ingest route is
// gated by the ServiceAuth port, because its caller is the ml server, not a user.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/mlops/internal/model"
)

// MLOpsAPI declares the mlops module's HTTP endpoints. Every method takes
// context.Context first; the caller's identity (admin JWT or the ml service
// token) travels on that context and is resolved through the AdminGuard and
// ServiceAuth ports, so it never appears as a parameter. Query parameters are
// parsed by the hand-written handler into the request struct the service reads.
type MLOpsAPI interface {
	// GET /admin/mlops/feature-rows
	ExportFeatureRows(ctx context.Context, req model.FeatureRowQuery) (model.FeatureRowExportResponse, error)

	// POST /admin/mlops/models
	RegisterModel(ctx context.Context, req model.RegisterModelRequest) (model.RegisterModelResponse, error)

	// POST /admin/mlops/models/:version/promote
	PromoteModel(ctx context.Context, version string, req model.PromoteModelRequest) (model.PromoteModelResponse, error)

	// GET /admin/mlops/canaries
	ListCanaries(ctx context.Context, req model.CanaryQuery) (model.CanaryListResponse, error)

	// POST /admin/mlops/canaries
	CreateCanary(ctx context.Context, req model.CreateCanaryRequest) (model.CanaryResponse, error)

	// POST /ml/predictions
	IngestPrediction(ctx context.Context, req model.PredictionLogRequest) (model.PredictionLogResponse, error)
}
