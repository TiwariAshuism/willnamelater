// Package scoring is the public seam of the scoring module: the only surface the
// composition root and the audit orchestrator import. It wires the
// module-private repository, service, and handler together and exposes the read
// routes, the Score method the orchestrator calls, and the cold-start /
// corpus-recompute maintenance hooks the scheduler drives.
//
// Everything behind it lives under internal/scoring/internal, which Go forbids
// any sibling module from importing. The cross-boundary value types (Score,
// FraudInput, Profiles) are re-exported from the dependency-free
// internal/scoring/contract leaf, so a caller that imports this package alone
// gets the whole vocabulary it needs.
package scoring

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/handler"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/repository"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/service"
)

// The scoring module's public value types, re-exported from the contract leaf so
// callers depend on this package alone.
type (
	// Score is the computed influence + authenticity result for one audit.
	Score = contract.Score
	// FraudInput is the ML fraud signal the orchestrator passes to Score.
	FraudInput = contract.FraudInput
	// Subscore is one component of the composite: value plus confidence.
	Subscore = contract.Subscore
	// Profiles is the port through which scoring resolves an influencer's niche.
	// The composition root wires it to the influencer module.
	Profiles = contract.Profiles
)

// Module is the wired scoring module. Construct it with New and mount it with
// RegisterRoutes.
type Module struct {
	handler *handler.Handler
	service *service.Service
}

// New wires the module over the shared pool. profiles resolves an influencer's
// niche for benchmark selection and may be nil, in which case scoring uses the
// baseline cohort for every audit.
func New(pool *db.Pool, profiles Profiles) *Module {
	svc := service.New(repository.New(pool), profiles)
	return &Module{
		handler: handler.New(svc),
		service: svc,
	}
}

// RegisterRoutes mounts the module's read routes on r (typically the /v1 group).
func (m *Module) RegisterRoutes(r gin.IRouter) {
	m.handler.Register(r)
}

// Score computes and persists the influence + authenticity score for one audit
// over whatever platform snapshots succeeded and the ML fraud signal. The audit
// orchestrator calls it through a Scorer port it declares; this method is that
// port's implementation. It is not an HTTP route.
func (m *Module) Score(
	ctx context.Context,
	auditJobID, influencerID uuid.UUID,
	snapshots []connector.Snapshot,
	fraud FraudInput,
) (Score, error) {
	return m.service.Score(ctx, auditJobID, influencerID, snapshots, fraud)
}

// NamedSubscore is one dimension of a persisted score, carrying the dimension
// name alongside its value and confidence. The exported Subscore alias omits the
// name (the persisted map is keyed on it), so ReportView reattaches it here for
// the report layer, which needs the label.
type NamedSubscore struct {
	Name       string
	Value      float64
	Confidence float64
}

// ReportView is a persisted score in the shape the report layer needs, exported
// so the composition root can assemble the advisory-report input without naming
// any scoring-internal type. It reads back the score the orchestrator just
// persisted (keyed on the audit's influencer), recovering the niche, tier,
// benchmark provenance, and per-dimension breakdown the narrow ScoreResult the
// orchestrator threads to the reporter deliberately drops.
type ReportView struct {
	Niche          string
	Tier           string
	Overall        float64
	Authenticity   float64
	BenchmarkLabel string
	Subscores      []NamedSubscore
}

// ReportView returns the latest persisted score for an influencer as a
// report-ready view. The composition root's Reporter adapter calls it to
// reconstruct the rich llm input from the score the Scorer persisted during the
// same audit. It is not an HTTP route.
func (m *Module) ReportView(ctx context.Context, influencerID uuid.UUID) (ReportView, error) {
	resp, err := m.service.GetLatestScore(ctx, influencerID.String())
	if err != nil {
		return ReportView{}, err
	}
	view := ReportView{
		Niche:          resp.Niche,
		Tier:           resp.Tier,
		Overall:        resp.Overall,
		BenchmarkLabel: resp.BenchmarkLabel,
		Subscores:      make([]NamedSubscore, 0, len(resp.Subscores)),
	}
	for name, sub := range resp.Subscores {
		view.Subscores = append(view.Subscores, NamedSubscore{Name: name, Value: sub.Value, Confidence: sub.Confidence})
		if name == "authenticity" {
			view.Authenticity = sub.Value
		}
	}
	return view, nil
}

// EnsureBootstrap seeds the cold-start weights and benchmarks if absent. It is
// idempotent; the composition root calls it on boot.
func (m *Module) EnsureBootstrap(ctx context.Context) error {
	return m.service.EnsureBootstrap(ctx)
}

// RecomputeCorpus republishes benchmarks from corpus percentiles for every cell
// that has reached the sample threshold, returning the number republished. The
// nightly scheduler calls it.
func (m *Module) RecomputeCorpus(ctx context.Context) (int, error) {
	return m.service.RecomputeCorpus(ctx)
}
