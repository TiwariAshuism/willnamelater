// Package api is the apigen source for the scoring module: an annotated Go
// interface from which the service interface is generated. The handler is
// hand-written (see internal/handler) so errors render through
// httpx.RenderError rather than the broken generated handler, and only the
// service layer is generated.
//
// The scoring module owns the score, scoring_weights, and benchmark tables. Its
// two read routes expose an influencer's latest score and score history; the
// write path (Score, called by the audit orchestrator) is a service method, not
// an HTTP route, and so is not declared here.
package api

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
)

// ScoringAPI is the scoring module's HTTP surface.
type ScoringAPI interface {
	// GET /influencers/:id/score
	GetLatestScore(ctx context.Context, id string) (model.ScoreResponse, error)

	// GET /influencers/:id/score/history
	GetScoreHistory(ctx context.Context, id string) (model.ScoreHistoryResponse, error)
}
