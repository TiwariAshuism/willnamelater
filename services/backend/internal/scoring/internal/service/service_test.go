package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/engine"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/repository"
)

// fakeRepo is an in-memory Repository. Its weight/benchmark lookups are keyed by
// "niche|tier" so a test can control exactly which cell resolves and assert the
// baseline fallback path.
type fakeRepo struct {
	weights    map[string]engine.Weights
	benchmarks map[string]engine.Benchmark

	latest  model.ScoreRow
	history []model.ScoreRow
	readErr error

	upserted []model.ScoreRow

	seededWeights    int
	seededBenchmarks int
	corpusCells      []repository.CorpusCell
	published        []publishCall
}

type publishCall struct {
	niche, tier string
	bench       engine.Benchmark
}

var _ repository.Repository = (*fakeRepo)(nil)

func key(niche, tier string) string { return niche + "|" + tier }

func (f *fakeRepo) ActiveWeights(_ context.Context, niche, tier string) (engine.Weights, bool, error) {
	w, ok := f.weights[key(niche, tier)]
	return w, ok, nil
}

func (f *fakeRepo) ActiveBenchmark(_ context.Context, niche, tier, _ string) (engine.Benchmark, bool, error) {
	b, ok := f.benchmarks[key(niche, tier)]
	return b, ok, nil
}

func (f *fakeRepo) UpsertScore(_ context.Context, row model.ScoreRow) error {
	f.upserted = append(f.upserted, row)
	return nil
}

func (f *fakeRepo) LatestScore(_ context.Context, _ uuid.UUID) (model.ScoreRow, error) {
	return f.latest, f.readErr
}

func (f *fakeRepo) ScoreHistory(_ context.Context, _ uuid.UUID, _ int) ([]model.ScoreRow, error) {
	return f.history, f.readErr
}

func (f *fakeRepo) InsertWeightsIfAbsent(_ context.Context, _, _ string, _ engine.Weights, _ bool) error {
	f.seededWeights++
	return nil
}

func (f *fakeRepo) InsertBenchmarkIfAbsent(_ context.Context, _, _ string, _ engine.Benchmark, _ bool) error {
	f.seededBenchmarks++
	return nil
}

func (f *fakeRepo) CorpusCells(_ context.Context, _ int) ([]repository.CorpusCell, error) {
	return f.corpusCells, nil
}

func (f *fakeRepo) PublishCorpusBenchmark(_ context.Context, niche, tier string, b engine.Benchmark) error {
	f.published = append(f.published, publishCall{niche: niche, tier: tier, bench: b})
	return nil
}

// fakeProfiles is a Profiles stand-in returning a fixed niche or a fixed error.
type fakeProfiles struct {
	niche string
	err   error
}

func (p fakeProfiles) NicheOf(context.Context, uuid.UUID) (string, error) {
	return p.niche, p.err
}

func bootstrapRepo() *fakeRepo {
	f := &fakeRepo{
		weights:    map[string]engine.Weights{},
		benchmarks: map[string]engine.Benchmark{},
	}
	f.weights[key(engine.BaselineNiche, engine.BaselineTier)] = engine.BootstrapWeights()
	for _, bb := range engine.BootstrapBenchmarks() {
		f.benchmarks[key(engine.BaselineNiche, bb.Tier)] = bb.Benchmark
	}
	return f
}

func sampleSnapshots() []connector.Snapshot {
	return []connector.Snapshot{{
		Platform:  connector.PlatformYouTube,
		Followers: 50_000,
		Posts:     []connector.Post{{Likes: 2_000, Comments: 120, Shares: 30}},
	}}
}

func TestScoreRequiresAuditID(t *testing.T) {
	t.Parallel()

	svc := New(bootstrapRepo(), nil)
	_, err := svc.Score(context.Background(), uuid.Nil, uuid.New(), sampleSnapshots(), contract.FraudInput{})
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

// TestScorePersistsAndStamps runs the happy path: a niche-specific weight/
// benchmark cell resolves, the engine runs, and the persisted row carries the
// version stamps and the contributing platform.
func TestScorePersistsAndStamps(t *testing.T) {
	t.Parallel()

	repo := bootstrapRepo()
	// A niche-specific cell that must win over the baseline fallback.
	nicheWeights := engine.BootstrapWeights()
	nicheWeights.Version = 42
	repo.weights[key("beauty", "micro")] = nicheWeights
	nicheBench := repo.benchmarks[key(engine.BaselineNiche, "micro")]
	nicheBench.Version = 9
	nicheBench.Source = "corpus"
	repo.benchmarks[key("beauty", "micro")] = nicheBench

	svc := New(repo, fakeProfiles{niche: "beauty"})
	auditID, infID := uuid.New(), uuid.New()

	score, err := svc.Score(context.Background(), auditID, infID, sampleSnapshots(),
		contract.FraudInput{Present: true, FakeFollowerRate: 0.02, Confidence: 0.6})
	if err != nil {
		t.Fatalf("score: %v", err)
	}

	if score.Niche != "beauty" || score.Tier != "micro" {
		t.Fatalf("cell = (%q,%q), want (beauty,micro)", score.Niche, score.Tier)
	}
	if score.WeightsVersion != 42 || score.BenchmarkVersion != 9 {
		t.Fatalf("versions = (%d,%d), want (42,9)", score.WeightsVersion, score.BenchmarkVersion)
	}
	if len(repo.upserted) != 1 {
		t.Fatalf("upserted %d rows, want 1", len(repo.upserted))
	}
	row := repo.upserted[0]
	if row.AuditJobID != auditID || row.InfluencerID == nil || *row.InfluencerID != infID {
		t.Fatalf("row identity mismatch: %+v", row)
	}
	if len(row.ContributingPlatforms) != 1 || row.ContributingPlatforms[0] != "youtube" {
		t.Fatalf("contributing = %v, want [youtube]", row.ContributingPlatforms)
	}
	if row.Breakdown.ObservedEngagementRate == nil {
		t.Fatal("observed engagement rate not persisted for corpus recompute")
	}
	if _, ok := row.Breakdown.Subscores[contract.ComponentConsistency]; !ok {
		t.Fatal("consistency subscore missing from breakdown")
	}
}

// TestScoreFallsBackToBaseline covers the cold-start path: no niche-specific
// cell, so both weights and benchmark resolve from the baseline cohort.
func TestScoreFallsBackToBaseline(t *testing.T) {
	t.Parallel()

	repo := bootstrapRepo()
	svc := New(repo, fakeProfiles{niche: "gaming"})

	score, err := svc.Score(context.Background(), uuid.New(), uuid.New(), sampleSnapshots(), contract.FraudInput{})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if score.BenchmarkLabel != engine.BootstrapLabel {
		t.Fatalf("label = %q, want %q", score.BenchmarkLabel, engine.BootstrapLabel)
	}
}

// TestScoreDefaultsNicheWhenNoPort confirms scoring works without a Profiles
// port wired: it uses the baseline niche.
func TestScoreDefaultsNicheWhenNoPort(t *testing.T) {
	t.Parallel()

	svc := New(bootstrapRepo(), nil)
	score, err := svc.Score(context.Background(), uuid.New(), uuid.New(), sampleSnapshots(), contract.FraudInput{})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if score.Niche != engine.BaselineNiche {
		t.Fatalf("niche = %q, want baseline", score.Niche)
	}
}

// TestScorePropagatesNicheError asserts a hard port failure is surfaced, not
// silently swallowed into the wrong cohort.
func TestScorePropagatesNicheError(t *testing.T) {
	t.Parallel()

	svc := New(bootstrapRepo(), fakeProfiles{err: errors.New("influencer service down")})
	_, err := svc.Score(context.Background(), uuid.New(), uuid.New(), sampleSnapshots(), contract.FraudInput{})
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("kind = %v, want KindUnavailable", errs.KindOf(err))
	}
}

// TestScoreErrorsWithoutWeights covers an unseeded deployment: no weights at all
// yields a typed unavailable error rather than a panic or a zero score.
func TestScoreErrorsWithoutWeights(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{weights: map[string]engine.Weights{}, benchmarks: map[string]engine.Benchmark{}}
	svc := New(repo, nil)
	_, err := svc.Score(context.Background(), uuid.New(), uuid.New(), sampleSnapshots(), contract.FraudInput{})
	if errs.KindOf(err) != errs.KindUnavailable {
		t.Fatalf("kind = %v, want KindUnavailable", errs.KindOf(err))
	}
}

func TestEnsureBootstrapSeedsEveryTier(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := New(repo, nil)
	if err := svc.EnsureBootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if repo.seededWeights != 1 {
		t.Fatalf("seeded %d weight sets, want 1", repo.seededWeights)
	}
	if repo.seededBenchmarks != len(engine.Tiers()) {
		t.Fatalf("seeded %d benchmarks, want %d", repo.seededBenchmarks, len(engine.Tiers()))
	}
}

// TestRecomputeCorpusPublishesReadyCells confirms only cells at/above the sample
// threshold are republished as corpus benchmarks.
func TestRecomputeCorpusPublishesReadyCells(t *testing.T) {
	t.Parallel()

	repo := bootstrapRepo()
	repo.corpusCells = []repository.CorpusCell{
		{Niche: "beauty", Tier: "micro", SampleSize: 42, P10: 0.01, P25: 0.02, P50: 0.03, P75: 0.05, P90: 0.08, Mean: 0.035},
		{Niche: "gaming", Tier: "mid", SampleSize: 31, P50: 0.02},
	}
	svc := New(repo, nil)

	n, err := svc.RecomputeCorpus(context.Background())
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if n != 2 || len(repo.published) != 2 {
		t.Fatalf("published %d cells, want 2", n)
	}
	if repo.published[0].bench.Source != "corpus" || repo.published[0].bench.Metric != engine.MetricEngagementRate {
		t.Fatalf("published benchmark not a corpus engagement_rate: %+v", repo.published[0].bench)
	}
}

func TestGetLatestScoreValidatesID(t *testing.T) {
	t.Parallel()

	svc := New(bootstrapRepo(), nil)
	if _, err := svc.GetLatestScore(context.Background(), "not-a-uuid"); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want KindInvalid", errs.KindOf(err))
	}
	if _, err := svc.GetScoreHistory(context.Background(), "bad"); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("history kind = %v, want KindInvalid", errs.KindOf(err))
	}
}

// TestGetLatestScoreMapsRow confirms the read DTO is assembled from the row and
// its breakdown.
func TestGetLatestScoreMapsRow(t *testing.T) {
	t.Parallel()

	infID := uuid.New()
	repo := bootstrapRepo()
	repo.latest = model.ScoreRow{
		AuditJobID:            uuid.New(),
		InfluencerID:          &infID,
		Overall:               71.5,
		WeightsVersion:        1,
		BenchmarkVersion:      1,
		ContributingPlatforms: []string{"youtube"},
		Breakdown: model.Breakdown{
			Niche:             "beauty",
			Tier:              "micro",
			OverallConfidence: 0.4,
			BenchmarkLabel:    engine.BootstrapLabel,
			Subscores:         map[string]contract.Subscore{contract.ComponentReach: {Value: 60, Confidence: 1}},
		},
	}
	svc := New(repo, nil)

	resp, err := svc.GetLatestScore(context.Background(), infID.String())
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if resp.Overall != 71.5 || resp.Confidence != 0.4 || resp.BenchmarkLabel != engine.BootstrapLabel {
		t.Fatalf("dto mismatch: %+v", resp)
	}
	if resp.InfluencerID != infID.String() {
		t.Fatalf("influencer id = %q, want %q", resp.InfluencerID, infID.String())
	}
}
