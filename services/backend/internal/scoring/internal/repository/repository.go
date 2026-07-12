// Package repository is the scoring module's data-access layer. It owns every
// SQL statement against the scoring_weights, benchmark, and score tables and maps
// rows to and from the module's value types. The service depends only on the
// Repository interface, so its logic is exercised against a fake with no live
// database.
package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/scoring/internal/engine"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
)

// Repository is the scoring module's persistence contract.
type Repository interface {
	// ActiveWeights returns the active weight set for a (niche, tier) cell. The
	// bool is false, with a nil error, when no active row exists for that cell, so
	// the service can fall back to the baseline cell.
	ActiveWeights(ctx context.Context, niche, tier string) (engine.Weights, bool, error)

	// ActiveBenchmark returns the active benchmark for a (niche, tier, metric)
	// cell, with the same not-found semantics as ActiveWeights.
	ActiveBenchmark(ctx context.Context, niche, tier, metric string) (engine.Benchmark, bool, error)

	// UpsertScore writes a computed score, keyed on audit_job_id: re-running the
	// scoring step for an audit overwrites its row rather than duplicating it.
	UpsertScore(ctx context.Context, row model.ScoreRow) error

	// LatestScore returns an influencer's most recent score, or errs.KindNotFound
	// when none exists.
	LatestScore(ctx context.Context, influencerID uuid.UUID) (model.ScoreRow, error)

	// ScoreHistory returns an influencer's scores, newest first, capped at limit.
	ScoreHistory(ctx context.Context, influencerID uuid.UUID, limit int) ([]model.ScoreRow, error)

	// InsertWeightsIfAbsent seeds a weight set for a cell at the given version,
	// doing nothing if that (niche, tier, version) already exists. It is the
	// idempotent cold-start seeding primitive.
	InsertWeightsIfAbsent(ctx context.Context, niche, tier string, w engine.Weights, active bool) error

	// InsertBenchmarkIfAbsent seeds a benchmark for a cell at the benchmark's
	// version, doing nothing if that (niche, tier, metric, version) already
	// exists.
	InsertBenchmarkIfAbsent(ctx context.Context, niche, tier string, b engine.Benchmark, active bool) error

	// CorpusObservations returns the reference population one row per DISTINCT
	// INFLUENCER (the newest score each has), restricted to scores whose data came
	// from a live, authenticated API pull. Aggregation into cells is the engine's
	// job (engine.CorpusCells), which de-duplicates again and enforces the same
	// provenance rule — the guarantee is too important to live only in SQL that no
	// unit test executes.
	CorpusObservations(ctx context.Context) ([]engine.CorpusObservation, error)

	// PublishCorpusBenchmark inserts a new benchmark version for a cell and makes
	// it the active one, deactivating the previous active row, in a single
	// transaction. The version is assigned as one past the cell's current maximum.
	PublishCorpusBenchmark(ctx context.Context, niche, tier string, b engine.Benchmark) error
}
