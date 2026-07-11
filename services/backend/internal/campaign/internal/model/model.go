// Package model holds the campaign module's request and response DTOs. They
// define the module's HTTP contract shape; the service that populates them is a
// scaffold returning errs.ErrNotImplemented until campaign management is built.
package model

import "time"

// CreateCampaignRequest is the body of POST /campaigns: a named group of
// influencers a brand tracks together.
type CreateCampaignRequest struct {
	Name          string   `json:"name" binding:"required"`
	InfluencerIDs []string `json:"influencer_ids"`
}

// CampaignResponse is one campaign and the influencers grouped under it.
type CampaignResponse struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	InfluencerIDs []string  `json:"influencer_ids"`
	CreatedAt     time.Time `json:"created_at"`
}
