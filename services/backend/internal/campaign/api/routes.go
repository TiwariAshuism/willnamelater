// Package api is the apigen source for the campaign module: an annotated Go
// interface from which the service interface is generated. The module groups
// influencers into named campaigns a brand tracks together. It is a scaffold —
// the service returns errs.ErrNotImplemented — so the shape exists and enabling
// it is a small change, but no route is mounted until campaign management is
// built.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/campaign/internal/model"
)

// CampaignAPI is the campaign module's HTTP surface. Every method takes
// context.Context first; the authenticated caller's identity travels on that
// context, so it never appears as a parameter.
type CampaignAPI interface {
	// POST /campaigns
	CreateCampaign(ctx context.Context, req model.CreateCampaignRequest) (model.CampaignResponse, error)

	// GET /campaigns
	ListCampaigns(ctx context.Context) ([]model.CampaignResponse, error)

	// GET /campaigns/:id
	GetCampaign(ctx context.Context, id string) (model.CampaignResponse, error)
}
