// Package service is the scoring module's business layer. It resolves the active
// weights and benchmark for an audit's (niche, tier) cell, drives the pure
// engine, persists the result, and serves the two read routes. It also owns the
// cold-start seeding and the corpus-recompute job the nightly scheduler calls.
package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/engine"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/repository"
)

// corpusMinSamples is the per-cell sample size at which a (niche, tier) cell has
// enough real audits to replace its bootstrap band with corpus percentiles.
const corpusMinSamples = 30

// Service implements the read API (ScoringService), the write path (Score) the
// audit orchestrator calls, and the cold-start / corpus maintenance jobs.
type Service struct {
	repo     repository.Repository
	profiles contract.Profiles
}

// New builds the scoring service. profiles resolves an influencer's niche and
// may be nil, in which case every audit falls back to the baseline benchmark
// cohort — the module still functions, it just cannot key benchmarks by niche.
func New(repo repository.Repository, profiles contract.Profiles) *Service {
	return &Service{repo: repo, profiles: profiles}
}

var _ ScoringService = (*Service)(nil)

// Score computes and persists the influence + authenticity score for one audit.
// It resolves the niche through the Profiles port, derives the tier from the
// live follower count, loads the active weights and engagement benchmark for
// that cell (falling back to the baseline cohort), runs the pure engine, and
// upserts the row keyed on audit_job_id so a re-run overwrites rather than
// duplicates.
func (s *Service) Score(
	ctx context.Context,
	auditJobID, influencerID uuid.UUID,
	snapshots []connector.Snapshot,
	fraud contract.FraudInput,
) (contract.Score, error) {
	if auditJobID == uuid.Nil {
		return contract.Score{}, errs.New(errs.KindInvalid, "scoring.audit_required", "an audit job id is required to score")
	}

	niche, err := s.resolveNiche(ctx, influencerID)
	if err != nil {
		return contract.Score{}, err
	}
	tier := engine.TierForFollowers(maxFollowers(snapshots))

	weights, err := s.resolveWeights(ctx, niche, tier)
	if err != nil {
		return contract.Score{}, err
	}
	benchmark, err := s.resolveBenchmark(ctx, niche, tier)
	if err != nil {
		return contract.Score{}, err
	}

	score, err := engine.Compute(engine.Input{
		Niche:               niche,
		Tier:                tier,
		Snapshots:           snapshots,
		Fraud:               fraud,
		Weights:             weights,
		EngagementBenchmark: benchmark,
	})
	if err != nil {
		return contract.Score{}, errs.Wrap(err, errs.KindInternal, "scoring.compute_failed", "could not compute score")
	}
	score.AuditJobID = auditJobID
	score.InfluencerID = influencerID

	if err := s.repo.UpsertScore(ctx, toScoreRow(score, snapshots)); err != nil {
		return contract.Score{}, err
	}
	return score, nil
}

// resolveNiche returns the influencer's niche through the port, or the baseline
// niche when no port is wired or the influencer has no niche on record. A hard
// port failure is propagated so scoring does not silently use the wrong cohort.
func (s *Service) resolveNiche(ctx context.Context, influencerID uuid.UUID) (string, error) {
	if s.profiles == nil || influencerID == uuid.Nil {
		return engine.BaselineNiche, nil
	}
	niche, err := s.profiles.NicheOf(ctx, influencerID)
	if err != nil {
		return "", errs.Wrap(err, errs.KindUnavailable, "scoring.niche_lookup", "could not resolve influencer niche")
	}
	if niche == "" {
		return engine.BaselineNiche, nil
	}
	return niche, nil
}

// resolveWeights returns the active weights for the exact cell, falling back to
// the baseline cell, and errors if neither exists (which means bootstrap seeding
// never ran).
func (s *Service) resolveWeights(ctx context.Context, niche, tier string) (engine.Weights, error) {
	if w, ok, err := s.repo.ActiveWeights(ctx, niche, tier); err != nil {
		return engine.Weights{}, err
	} else if ok {
		return w, nil
	}
	if w, ok, err := s.repo.ActiveWeights(ctx, engine.BaselineNiche, engine.BaselineTier); err != nil {
		return engine.Weights{}, err
	} else if ok {
		return w, nil
	}
	return engine.Weights{}, errs.New(errs.KindUnavailable, "scoring.weights_unavailable", "no active scoring weights are configured")
}

// resolveBenchmark returns the active engagement benchmark for the exact cell,
// falling back to the baseline niche at the same tier (the cold-start bands are
// seeded per tier under the baseline niche).
func (s *Service) resolveBenchmark(ctx context.Context, niche, tier string) (engine.Benchmark, error) {
	metric := engine.MetricEngagementRate
	if b, ok, err := s.repo.ActiveBenchmark(ctx, niche, tier, metric); err != nil {
		return engine.Benchmark{}, err
	} else if ok {
		return b, nil
	}
	if b, ok, err := s.repo.ActiveBenchmark(ctx, engine.BaselineNiche, tier, metric); err != nil {
		return engine.Benchmark{}, err
	} else if ok {
		return b, nil
	}
	return engine.Benchmark{}, errs.New(errs.KindUnavailable, "scoring.benchmark_unavailable", "no active engagement benchmark is configured")
}

// EnsureBootstrap seeds the cold-start weight set and engagement benchmarks if
// they are absent. It is idempotent: re-running it inserts nothing, so the
// composition root can call it on every boot. The seeded benchmarks are
// provenance-labelled industry reference bands (see the engine's bootstrap
// constants), not fabricated user data.
func (s *Service) EnsureBootstrap(ctx context.Context) error {
	if err := s.repo.InsertWeightsIfAbsent(ctx, engine.BaselineNiche, engine.BaselineTier, engine.BootstrapWeights(), true); err != nil {
		return err
	}
	for _, bb := range engine.BootstrapBenchmarks() {
		if err := s.repo.InsertBenchmarkIfAbsent(ctx, engine.BaselineNiche, bb.Tier, bb.Benchmark, true); err != nil {
			return err
		}
	}
	return nil
}

// RecomputeCorpus replaces bootstrap bands with corpus-derived percentiles for
// every (niche, tier) cell that has reached corpusMinSamples persisted scores,
// publishing each as a new active source='corpus' benchmark version. The nightly
// scheduler calls it; it returns the number of cells republished.
func (s *Service) RecomputeCorpus(ctx context.Context) (int, error) {
	cells, err := s.repo.CorpusCells(ctx, corpusMinSamples)
	if err != nil {
		return 0, err
	}
	var published int
	for _, c := range cells {
		b := engine.Benchmark{
			Metric:     engine.MetricEngagementRate,
			P10:        c.P10,
			P25:        c.P25,
			P50:        c.P50,
			P75:        c.P75,
			P90:        c.P90,
			Mean:       c.Mean,
			Stddev:     c.Stddev,
			SampleSize: c.SampleSize,
			Source:     "corpus",
		}
		if err := s.repo.PublishCorpusBenchmark(ctx, c.Niche, c.Tier, b); err != nil {
			return published, err
		}
		published++
	}
	return published, nil
}

// GetLatestScore returns an influencer's most recent score.
func (s *Service) GetLatestScore(ctx context.Context, id string) (model.ScoreResponse, error) {
	influencerID, err := uuid.Parse(id)
	if err != nil {
		return model.ScoreResponse{}, errs.New(errs.KindInvalid, "scoring.invalid_influencer_id", "influencer id must be a uuid")
	}
	row, err := s.repo.LatestScore(ctx, influencerID)
	if err != nil {
		return model.ScoreResponse{}, err
	}
	return toScoreResponse(row), nil
}

// GetScoreHistory returns an influencer's scores over time, newest first.
func (s *Service) GetScoreHistory(ctx context.Context, id string) (model.ScoreHistoryResponse, error) {
	influencerID, err := uuid.Parse(id)
	if err != nil {
		return model.ScoreHistoryResponse{}, errs.New(errs.KindInvalid, "scoring.invalid_influencer_id", "influencer id must be a uuid")
	}
	rows, err := s.repo.ScoreHistory(ctx, influencerID, 0)
	if err != nil {
		return model.ScoreHistoryResponse{}, err
	}
	points := make([]model.ScorePoint, 0, len(rows))
	for _, r := range rows {
		points = append(points, model.ScorePoint{
			AuditJobID: r.AuditJobID.String(),
			Overall:    r.Overall,
			Confidence: r.Breakdown.OverallConfidence,
			CreatedAt:  r.CreatedAt,
		})
	}
	return model.ScoreHistoryResponse{InfluencerID: influencerID.String(), Points: points}, nil
}

// maxFollowers returns the largest follower count across the snapshots, which
// sets the audience-size tier.
func maxFollowers(snaps []connector.Snapshot) int64 {
	var largest int64
	for _, s := range snaps {
		if s.Followers > largest {
			largest = s.Followers
		}
	}
	return largest
}
