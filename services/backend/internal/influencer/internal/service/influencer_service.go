// Package service is the influencer module's business layer. It validates and
// normalizes input, then delegates persistence to an InfluencerRepository. The
// rules enforced here are load-bearing: niche is stored as free text, so an
// unrecognized value must be rejected before it is written, and platform is a
// database enum, so an unknown platform must become a 400 rather than a 500.
//
// Tier is never validated or accepted from clients here because no request
// carries it; it is derived in the repository from handle follower counts.
package service

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/influencer/internal/model"
	"github.com/getnyx/influaudit/backend/internal/influencer/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// List page-size bounds. A request that omits or overshoots the limit is served
// defaultListLimit; the service never returns an unbounded page.
const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// influencerService is the InfluencerService implementation.
type influencerService struct {
	repo repository.InfluencerRepository
}

var _ InfluencerService = (*influencerService)(nil)

// New builds the InfluencerService backed by repo.
func New(repo repository.InfluencerRepository) InfluencerService {
	return &influencerService{repo: repo}
}

// CreateInfluencer validates the optional niche and country, then persists.
func (s *influencerService) CreateInfluencer(ctx context.Context, req model.CreateInfluencerRequest) (model.InfluencerResponse, error) {
	if err := validateNiche(req.Niche); err != nil {
		return model.InfluencerResponse{}, err
	}
	country, err := normalizeCountry(req.Country)
	if err != nil {
		return model.InfluencerResponse{}, err
	}
	req.Country = country

	return s.repo.CreateInfluencer(ctx, req)
}

// GetInfluencer validates the id and returns the influencer with its handles.
func (s *influencerService) GetInfluencer(ctx context.Context, id string) (model.InfluencerResponse, error) {
	canonical, err := parseID(id, "influencer.id_invalid", "influencer id is not a valid identifier")
	if err != nil {
		return model.InfluencerResponse{}, err
	}
	return s.repo.GetInfluencer(ctx, canonical)
}

// ListInfluencers validates the filters, clamps the page size, and delegates.
func (s *influencerService) ListInfluencers(ctx context.Context, req model.ListInfluencersRequest) (model.ListInfluencersResponse, error) {
	if err := validateNiche(req.Niche); err != nil {
		return model.ListInfluencersResponse{}, err
	}
	if req.Tier != nil {
		if t := model.Tier(*req.Tier); !t.Valid() {
			return model.ListInfluencersResponse{}, errs.New(errs.KindInvalid, "influencer.tier_invalid", "tier is not a recognized band")
		}
	}
	req.Limit = clampLimit(req.Limit)

	return s.repo.ListInfluencers(ctx, req)
}

// UpdateInfluencer validates the id and the fields present in a partial update.
func (s *influencerService) UpdateInfluencer(ctx context.Context, id string, req model.UpdateInfluencerRequest) (model.InfluencerResponse, error) {
	canonical, err := parseID(id, "influencer.id_invalid", "influencer id is not a valid identifier")
	if err != nil {
		return model.InfluencerResponse{}, err
	}
	if err := validateNiche(req.Niche); err != nil {
		return model.InfluencerResponse{}, err
	}
	country, err := normalizeCountry(req.Country)
	if err != nil {
		return model.InfluencerResponse{}, err
	}
	req.Country = country

	return s.repo.UpdateInfluencer(ctx, canonical, req)
}

// AddHandle validates the id, platform, and handle before persisting.
func (s *influencerService) AddHandle(ctx context.Context, id string, req model.AddHandleRequest) (model.HandleResponse, error) {
	canonical, err := parseID(id, "influencer.id_invalid", "influencer id is not a valid identifier")
	if err != nil {
		return model.HandleResponse{}, err
	}

	platform, err := model.ParsePlatform(req.Platform)
	if err != nil {
		return model.HandleResponse{}, err
	}
	req.Platform = string(platform)

	req.Handle = strings.TrimSpace(req.Handle)
	if req.Handle == "" {
		return model.HandleResponse{}, errs.New(errs.KindInvalid, "influencer.handle_required", "handle is required")
	}

	return s.repo.AddHandle(ctx, canonical, req)
}

// DeleteHandle validates both identifiers and delegates the removal.
func (s *influencerService) DeleteHandle(ctx context.Context, id string, handleID string) error {
	canonicalID, err := parseID(id, "influencer.id_invalid", "influencer id is not a valid identifier")
	if err != nil {
		return err
	}
	canonicalHandleID, err := parseID(handleID, "influencer.handle_id_invalid", "handle id is not a valid identifier")
	if err != nil {
		return err
	}
	return s.repo.DeleteHandle(ctx, canonicalID, canonicalHandleID)
}

// validateNiche rejects a present-but-unrecognized niche. A nil pointer means
// the caller omitted the field, which is allowed.
func validateNiche(raw *string) error {
	if raw == nil {
		return nil
	}
	_, err := model.ParseNiche(*raw)
	return err
}

// normalizeCountry validates an optional ISO 3166-1 alpha-2 code and returns it
// upper-cased. A nil pointer passes through unchanged; a present value must be
// exactly two ASCII letters.
func normalizeCountry(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	code := strings.TrimSpace(*raw)
	if len(code) != 2 || !isAlpha(code) {
		return nil, errs.New(errs.KindInvalid, "influencer.country_invalid", "country must be an ISO 3166-1 alpha-2 code")
	}
	upper := strings.ToUpper(code)
	return &upper, nil
}

// isAlpha reports whether s consists solely of ASCII letters.
func isAlpha(s string) bool {
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

// clampLimit bounds a requested page size into [1, maxListLimit], defaulting a
// non-positive request to defaultListLimit.
func clampLimit(requested int) int {
	switch {
	case requested <= 0:
		return defaultListLimit
	case requested > maxListLimit:
		return maxListLimit
	default:
		return requested
	}
}

// parseID validates raw as a UUID and returns its canonical string form,
// mapping a malformed value to errs.KindInvalid with the given code.
func parseID(raw, code, message string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", errs.New(errs.KindInvalid, code, message)
	}
	return id.String(), nil
}
