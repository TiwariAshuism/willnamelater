// Package service normalizes and stores a creator's uploaded platform data. It
// parses the upload into the connector's snapshot vocabulary, attaches a single
// follower metric point, and persists the dataset against the authenticated
// caller. It fabricates nothing: the follower count and every post come from the
// upload, and a CSV upload carries no comments, so the stored dataset has none —
// which is why an Instagram audit from this path is partial for the coordination
// signal rather than inventing one.
package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/model"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/parser"
	"github.com/getnyx/influaudit/backend/internal/dataimport/port"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// sourceInstagramCSV labels a dataset uploaded as an Instagram Insights CSV.
const sourceInstagramCSV = "instagram_csv"

// datasetStore persists normalized datasets. The repository satisfies it; the
// interface lets the service's normalization be tested without a database.
type datasetStore interface {
	Insert(ctx context.Context, userID, influencerID uuid.UUID, platform connector.Platform, ds model.Dataset) (uuid.UUID, error)
}

// Service handles uploads over the store and caller identity.
type Service struct {
	repo     datasetStore
	identity port.Identity
	now      func() time.Time
}

// New wires the service. now is injectable so tests pin the capture time.
func New(repo datasetStore, identity port.Identity) *Service {
	return &Service{repo: repo, identity: identity, now: time.Now}
}

// ImportInstagramCSV parses the uploaded Instagram posts CSV, normalizes it into
// a dataset, and stores it against the authenticated caller for the named
// influencer. The stored dataset carries the parsed posts, one follower metric
// point, and no comments.
func (s *Service) ImportInstagramCSV(ctx context.Context, req model.ImportRequest) (model.ImportResponse, error) {
	userID, err := s.identity.CallerID(ctx)
	if err != nil {
		return model.ImportResponse{}, err
	}

	influencerID, err := uuid.Parse(req.InfluencerID)
	if err != nil {
		return model.ImportResponse{}, errs.New(errs.KindInvalid, "dataimport.invalid_influencer_id", "influencer id must be a uuid")
	}

	handle := strings.TrimSpace(req.Handle)
	if handle == "" {
		return model.ImportResponse{}, errs.New(errs.KindInvalid, "dataimport.missing_handle", "handle is required")
	}

	posts, err := parser.ParseInstagramPostsCSV(strings.NewReader(req.PostsCSV))
	if err != nil {
		return model.ImportResponse{}, err
	}

	capturedAt := s.now().UTC()
	ds := model.Dataset{
		Handle:     handle,
		Followers:  req.Followers,
		Source:     sourceInstagramCSV,
		CapturedAt: capturedAt,
		Posts:      posts,
		Metrics: []connector.MetricPoint{
			{At: capturedAt, Name: "followers", Value: float64(req.Followers)},
		},
		Comments: nil,
	}

	id, err := s.repo.Insert(ctx, userID, influencerID, connector.PlatformInstagram, ds)
	if err != nil {
		return model.ImportResponse{}, err
	}

	return model.ImportResponse{
		DatasetID: id.String(),
		Platform:  string(connector.PlatformInstagram),
		Handle:    handle,
		Posts:     len(posts),
	}, nil
}
