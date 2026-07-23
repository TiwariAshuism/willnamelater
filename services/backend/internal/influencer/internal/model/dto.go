// Package model holds the influencer module's domain types and the request and
// response DTOs its API exposes. Tier is derived from follower count by a pure
// function rather than trusted from the client.
package model

import "time"

// CreateInfluencerRequest is the body of POST /influencers. Every field is
// optional at the schema level (the columns are nullable); niche, when present,
// must be a recognized category. Tier is absent by design: it is derived from
// handles, never supplied here.
type CreateInfluencerRequest struct {
	DisplayName *string `json:"display_name"`
	Niche       *string `json:"niche"`
	Country     *string `json:"country"`
}

// UpdateInfluencerRequest is the body of PATCH /influencers/:id. It carries
// PATCH semantics: a nil field is left unchanged. Tier is not updatable here
// because it is derived from handles.
type UpdateInfluencerRequest struct {
	DisplayName *string `json:"display_name"`
	Niche       *string `json:"niche"`
	Country     *string `json:"country"`
}

// ListInfluencersRequest is the query for GET /influencers: optional niche and
// tier filters plus keyset pagination.
type ListInfluencersRequest struct {
	Niche *string `form:"niche"`
	Tier  *string `form:"tier"`
	// Limit bounds the page size. Zero or out-of-range values are clamped by the
	// service to a sane default and maximum.
	Limit int `form:"limit"`
	// Cursor is the opaque keyset token returned as NextCursor by the previous
	// page. Empty requests the first page.
	Cursor string `form:"cursor"`
}

// AddHandleRequest is the body of POST /influencers/:id/handles. FollowerCount,
// when present, feeds the derived tier of the owning influencer.
type AddHandleRequest struct {
	Platform       string  `json:"platform"`
	Handle         string  `json:"handle"`
	PlatformUserID *string `json:"platform_user_id"`
	FollowerCount  *int64  `json:"follower_count"`
	Verified       bool    `json:"verified"`
}

// InfluencerResponse is the wire representation of an Influencer. Handles is
// populated for single-resource responses and omitted from list rows.
type InfluencerResponse struct {
	ID          string           `json:"id"`
	DisplayName *string          `json:"display_name,omitempty"`
	Niche       *string          `json:"niche,omitempty"`
	Tier        *string          `json:"tier,omitempty"`
	Country     *string          `json:"country,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Handles     []HandleResponse `json:"handles,omitempty"`
}

// HandleResponse is the wire representation of a Handle.
type HandleResponse struct {
	ID             string     `json:"id"`
	InfluencerID   string     `json:"influencer_id"`
	Platform       string     `json:"platform"`
	Handle         string     `json:"handle"`
	PlatformUserID *string    `json:"platform_user_id,omitempty"`
	FollowerCount  *int64     `json:"follower_count,omitempty"`
	Verified       bool       `json:"verified"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ListInfluencersResponse wraps a page of influencers with the cursor that
// fetches the next page. NextCursor is empty when the page is the last one.
type ListInfluencersResponse struct {
	Influencers []InfluencerResponse `json:"influencers"`
	NextCursor  string               `json:"next_cursor,omitempty"`
}
