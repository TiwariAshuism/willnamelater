// Package api is the apigen source for the influencer module. It declares the
// HTTP surface as an annotated Go interface; apigen derives the service and
// repository interfaces from it. The handler is hand-written (see
// internal/handler) so errors render through httpx.RenderError rather than the
// broken generated handler.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
)

// InfluencerAPI is the influencer module's HTTP contract.
type InfluencerAPI interface {
	// POST /influencers
	CreateInfluencer(ctx context.Context, req model.CreateInfluencerRequest) (model.InfluencerResponse, error)

	// GET /influencers/:id
	GetInfluencer(ctx context.Context, id string) (model.InfluencerResponse, error)

	// GET /influencers
	ListInfluencers(ctx context.Context, req model.ListInfluencersRequest) (model.ListInfluencersResponse, error)

	// PATCH /influencers/:id
	UpdateInfluencer(ctx context.Context, id string, req model.UpdateInfluencerRequest) (model.InfluencerResponse, error)

	// POST /influencers/:id/handles
	AddHandle(ctx context.Context, id string, req model.AddHandleRequest) (model.HandleResponse, error)

	// DELETE /influencers/:id/handles/:handleID
	DeleteHandle(ctx context.Context, id string, handleID string) error
}
