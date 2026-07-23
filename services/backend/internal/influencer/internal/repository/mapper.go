package repository

import (
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
)

// scannable is the read side of pgx.Row and pgx.Rows, letting the scan helpers
// serve both a single-row query and a row within an iteration.
type scannable interface {
	Scan(dest ...any) error
}

// scanInfluencer reads one influencer row in influencerColumns order. The id is
// selected as text (id::text) so it scans into a string independent of the pgx
// uuid codec, then parsed into a uuid.UUID.
func scanInfluencer(row scannable) (model.Influencer, error) {
	var (
		inf    model.Influencer
		idText string
		niche  *string
		tier   *string
	)
	if err := row.Scan(&idText, &inf.DisplayName, &niche, &tier, &inf.Country, &inf.CreatedAt, &inf.UpdatedAt); err != nil {
		return model.Influencer{}, err
	}

	id, err := uuid.Parse(idText)
	if err != nil {
		return model.Influencer{}, err
	}
	inf.ID = id

	if niche != nil {
		n := model.Niche(*niche)
		inf.Niche = &n
	}
	if tier != nil {
		t := model.Tier(*tier)
		inf.Tier = &t
	}

	return inf, nil
}

// scanHandle reads one handle row in handleColumns order.
func scanHandle(row scannable) (model.Handle, error) {
	var (
		h            model.Handle
		idText       string
		influencerID string
		platformText string
	)
	if err := row.Scan(&idText, &influencerID, &platformText, &h.Handle, &h.PlatformUserID,
		&h.FollowerCount, &h.Verified, &h.LastSeenAt, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return model.Handle{}, err
	}

	id, err := uuid.Parse(idText)
	if err != nil {
		return model.Handle{}, err
	}
	ownerID, err := uuid.Parse(influencerID)
	if err != nil {
		return model.Handle{}, err
	}
	h.ID = id
	h.InfluencerID = ownerID
	h.Platform = connector.Platform(platformText)

	return h, nil
}

// buildListResponse trims an over-fetched slice to the page size and derives the
// next cursor. influencers holds up to limit+1 rows; the extra row signals a
// further page and its predecessor's position becomes the cursor.
func buildListResponse(influencers []model.Influencer, limit int) model.ListInfluencersResponse {
	resp := model.ListInfluencersResponse{Influencers: make([]model.InfluencerResponse, 0, limit)}

	page := influencers
	hasMore := len(influencers) > limit
	if hasMore {
		page = influencers[:limit]
	}

	for _, inf := range page {
		resp.Influencers = append(resp.Influencers, toInfluencerResponse(inf))
	}

	if hasMore {
		last := page[len(page)-1]
		resp.NextCursor = encodeCursor(cursor{createdAt: last.CreatedAt, id: last.ID})
	}

	return resp
}

// toInfluencerResponse maps a domain Influencer to its wire representation.
func toInfluencerResponse(inf model.Influencer) model.InfluencerResponse {
	resp := model.InfluencerResponse{
		ID:          inf.ID.String(),
		DisplayName: inf.DisplayName,
		Country:     inf.Country,
		CreatedAt:   inf.CreatedAt,
		UpdatedAt:   inf.UpdatedAt,
	}
	if inf.Niche != nil {
		niche := string(*inf.Niche)
		resp.Niche = &niche
	}
	if inf.Tier != nil {
		tier := string(*inf.Tier)
		resp.Tier = &tier
	}
	if len(inf.Handles) > 0 {
		resp.Handles = make([]model.HandleResponse, 0, len(inf.Handles))
		for _, h := range inf.Handles {
			resp.Handles = append(resp.Handles, toHandleResponse(h))
		}
	}
	return resp
}

// toHandleResponse maps a domain Handle to its wire representation.
func toHandleResponse(h model.Handle) model.HandleResponse {
	return model.HandleResponse{
		ID:             h.ID.String(),
		InfluencerID:   h.InfluencerID.String(),
		Platform:       string(h.Platform),
		Handle:         h.Handle,
		PlatformUserID: h.PlatformUserID,
		FollowerCount:  h.FollowerCount,
		Verified:       h.Verified,
		LastSeenAt:     h.LastSeenAt,
		CreatedAt:      h.CreatedAt,
		UpdatedAt:      h.UpdatedAt,
	}
}
