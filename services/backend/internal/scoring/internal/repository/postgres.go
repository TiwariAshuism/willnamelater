package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/platform/db"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/scoring/contract"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/engine"
	"github.com/getnyx/influaudit/backend/internal/scoring/internal/model"
)

// maxHistory bounds a history read so an influencer with a long audit tail
// cannot ask for an unbounded scan.
const maxHistory = 500

// PostgresRepository is the pgx-backed Repository.
type PostgresRepository struct {
	pool *db.Pool
}

// New builds a PostgresRepository over the shared pool.
func New(pool *db.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

var _ Repository = (*PostgresRepository)(nil)

// weightsJSON is the wire shape of the scoring_weights.weights column: a
// factor-keyed object read back into an engine.Weights. The keys are the v2
// hireability factors; a v1 (influence) weight row decodes to all-zero here and
// is never active after migration 000030.
type weightsJSON struct {
	EngagementAuthenticity float64 `json:"engagement_authenticity"`
	AudienceQuality        float64 `json:"audience_quality"`
	ConsistencyReliability float64 `json:"consistency_reliability"`
	BrandFitClarity        float64 `json:"brand_fit_clarity"`
}

// ActiveWeights reads the active weight set for a (niche, tier) cell.
func (r *PostgresRepository) ActiveWeights(ctx context.Context, niche, tier string) (engine.Weights, bool, error) {
	const q = `SELECT weights, version FROM scoring_weights
	           WHERE niche = $1 AND tier = $2 AND active LIMIT 1`

	var raw []byte
	var version int
	err := r.pool.QueryRow(ctx, q, niche, tier).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.Weights{}, false, nil
	}
	if err != nil {
		return engine.Weights{}, false, errs.Wrap(err, errs.KindUnavailable, "scoring.query_weights", "could not read scoring weights")
	}

	var wj weightsJSON
	if err := json.Unmarshal(raw, &wj); err != nil {
		return engine.Weights{}, false, errs.Wrap(err, errs.KindInternal, "scoring.decode_weights", "could not decode scoring weights")
	}
	return engine.Weights{
		EngagementAuthenticity: wj.EngagementAuthenticity,
		AudienceQuality:        wj.AudienceQuality,
		ConsistencyReliability: wj.ConsistencyReliability,
		BrandFitClarity:        wj.BrandFitClarity,
		Version:                version,
	}, true, nil
}

// ActiveBenchmark reads the active benchmark for a (niche, tier, metric) cell.
func (r *PostgresRepository) ActiveBenchmark(ctx context.Context, niche, tier, metric string) (engine.Benchmark, bool, error) {
	const q = `SELECT p10, p25, p50, p75, p90, mean, stddev, sample_size, version, source
	           FROM benchmark
	           WHERE niche = $1 AND tier = $2 AND metric = $3 AND active LIMIT 1`

	var (
		b          engine.Benchmark
		sampleSize *int
	)
	b.Metric = metric
	err := r.pool.QueryRow(ctx, q, niche, tier, metric).Scan(
		&b.P10, &b.P25, &b.P50, &b.P75, &b.P90, &b.Mean, &b.Stddev, &sampleSize, &b.Version, &b.Source)
	if errors.Is(err, pgx.ErrNoRows) {
		return engine.Benchmark{}, false, nil
	}
	if err != nil {
		return engine.Benchmark{}, false, errs.Wrap(err, errs.KindUnavailable, "scoring.query_benchmark", "could not read benchmark")
	}
	if sampleSize != nil {
		b.SampleSize = *sampleSize
	}
	b.Label = engine.BenchmarkLabelFor(b.Source, b.Version)
	return b, true, nil
}

// UpsertScore writes a score row, overwriting any existing row for the audit.
func (r *PostgresRepository) UpsertScore(ctx context.Context, row model.ScoreRow) error {
	breakdown, err := row.Breakdown.Marshal()
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "scoring.encode_breakdown", "could not encode score breakdown")
	}

	const q = `INSERT INTO score
		(audit_job_id, influencer_id, overall, engagement_authenticity, audience_quality,
		 consistency, brand_fit, weights_version, benchmark_version, contributing_platforms, breakdown,
		 verification_tier)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::platform[], $11::jsonb, $12)
		ON CONFLICT (audit_job_id) DO UPDATE SET
			influencer_id           = EXCLUDED.influencer_id,
			overall                 = EXCLUDED.overall,
			engagement_authenticity = EXCLUDED.engagement_authenticity,
			audience_quality        = EXCLUDED.audience_quality,
			consistency             = EXCLUDED.consistency,
			brand_fit               = EXCLUDED.brand_fit,
			weights_version         = EXCLUDED.weights_version,
			benchmark_version       = EXCLUDED.benchmark_version,
			contributing_platforms  = EXCLUDED.contributing_platforms,
			breakdown               = EXCLUDED.breakdown,
			verification_tier       = EXCLUDED.verification_tier`

	platforms := row.ContributingPlatforms
	if platforms == nil {
		platforms = []string{}
	}
	tier := row.VerificationTier
	if tier == "" {
		tier = contract.VerificationUnverified
	}
	if _, err := r.pool.Exec(ctx, q,
		row.AuditJobID, row.InfluencerID, row.Overall, row.EngagementAuthenticity,
		row.AudienceQuality, row.Consistency, row.BrandFit, row.WeightsVersion, row.BenchmarkVersion,
		platforms, breakdown, tier,
	); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "scoring.upsert_score", "could not persist score")
	}
	return nil
}

const scoreColumns = `audit_job_id, influencer_id, overall, weights_version, benchmark_version,
	contributing_platforms, breakdown, verification_tier, created_at`

// LatestScore reads an influencer's most recent score row.
func (r *PostgresRepository) LatestScore(ctx context.Context, influencerID uuid.UUID) (model.ScoreRow, error) {
	const q = `SELECT ` + scoreColumns + ` FROM score
	           WHERE influencer_id = $1 ORDER BY created_at DESC LIMIT 1`

	row, err := scanScore(r.pool.QueryRow(ctx, q, influencerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ScoreRow{}, errs.New(errs.KindNotFound, "scoring.score_not_found", "no score exists for this influencer")
	}
	if err != nil {
		return model.ScoreRow{}, err
	}
	return row, nil
}

// ScoreHistory reads an influencer's scores, newest first, capped at limit.
func (r *PostgresRepository) ScoreHistory(ctx context.Context, influencerID uuid.UUID, limit int) ([]model.ScoreRow, error) {
	if limit <= 0 || limit > maxHistory {
		limit = maxHistory
	}
	const q = `SELECT ` + scoreColumns + ` FROM score
	           WHERE influencer_id = $1 ORDER BY created_at DESC LIMIT $2`

	rows, err := r.pool.Query(ctx, q, influencerID, limit)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "scoring.query_history", "could not read score history")
	}
	defer rows.Close()

	out := make([]model.ScoreRow, 0)
	for rows.Next() {
		row, scanErr := scanScore(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "scoring.rows_history", "could not read score history")
	}
	return out, nil
}

// scanRow is the subset of pgx.Row and pgx.Rows this repository scans through.
type scanRow interface {
	Scan(dest ...any) error
}

func scanScore(row scanRow) (model.ScoreRow, error) {
	var (
		out       model.ScoreRow
		breakdown []byte
	)
	if err := row.Scan(
		&out.AuditJobID, &out.InfluencerID, &out.Overall, &out.WeightsVersion,
		&out.BenchmarkVersion, &out.ContributingPlatforms, &breakdown, &out.VerificationTier, &out.CreatedAt,
	); err != nil {
		return model.ScoreRow{}, err
	}
	if len(breakdown) > 0 {
		if err := json.Unmarshal(breakdown, &out.Breakdown); err != nil {
			return model.ScoreRow{}, errs.Wrap(err, errs.KindInternal, "scoring.decode_breakdown", "could not decode score breakdown")
		}
	}
	return out, nil
}

// InsertWeightsIfAbsent seeds a weight set, doing nothing if it already exists.
func (r *PostgresRepository) InsertWeightsIfAbsent(ctx context.Context, niche, tier string, w engine.Weights, active bool) error {
	payload, err := json.Marshal(weightsJSON{
		EngagementAuthenticity: w.EngagementAuthenticity,
		AudienceQuality:        w.AudienceQuality,
		ConsistencyReliability: w.ConsistencyReliability,
		BrandFitClarity:        w.BrandFitClarity,
	})
	if err != nil {
		return errs.Wrap(err, errs.KindInternal, "scoring.encode_weights", "could not encode weights")
	}

	const q = `INSERT INTO scoring_weights (niche, tier, version, weights, active, notes)
	           VALUES ($1, $2, $3, $4::jsonb, $5, $6)
	           ON CONFLICT (niche, tier, version) DO NOTHING`
	if _, err := r.pool.Exec(ctx, q, niche, tier, w.Version, payload, active, "creator-score 4-factor v2"); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "scoring.seed_weights", "could not seed scoring weights")
	}
	return nil
}

// InsertBenchmarkIfAbsent seeds a benchmark, doing nothing if it already exists.
// A bootstrap band's sample_size is written as SQL NULL (see nullableSamples): it
// rests on zero observations, and a band that counted nobody must not carry a
// count.
func (r *PostgresRepository) InsertBenchmarkIfAbsent(ctx context.Context, niche, tier string, b engine.Benchmark, active bool) error {
	const q = `INSERT INTO benchmark
		(niche, tier, metric, version, source, p10, p25, p50, p75, p90, mean, stddev, sample_size, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (niche, tier, metric, version) DO NOTHING`
	if _, err := r.pool.Exec(ctx, q,
		niche, tier, b.Metric, b.Version, b.Source,
		b.P10, b.P25, b.P50, b.P75, b.P90, b.Mean, b.Stddev, nullableSamples(b.SampleSize), active,
	); err != nil {
		return errs.Wrap(err, errs.KindUnavailable, "scoring.seed_benchmark", "could not seed benchmark")
	}
	return nil
}

// nullableSamples renders a sample size for the nullable benchmark.sample_size
// column: a real count, or NULL when nothing was counted. Zero and NULL are not
// the same claim, and "no observations" is the claim a bootstrap band has to make.
func nullableSamples(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// CorpusObservations reads the reference population: ONE ROW PER DISTINCT
// INFLUENCER — the newest score that influencer has — restricted to scores whose
// data came from a live, authenticated API pull.
//
// It replaces an aggregation that did count(*) over the score table with no
// DISTINCT, so thirty re-audits of one creator published a source='corpus'
// benchmark with sample_size 30 that every other creator was then percentiled
// against. DISTINCT ON (influencer_id) is therefore load-bearing, not an
// optimisation.
//
// The WHERE clause is OBSERVATION-ONLY: it turns on who the row is about
// (influencer_id), how its data was obtained (verification_tier), and whether the
// metric exists. It must NEVER filter on a fraud-score-derived attribute — no
// authenticity floor, no risk-score ceiling, no verdict. Excluding accounts our own
// heuristic dislikes would make the reference population a function of the
// heuristic's output, and the percentiles would then only ever confirm it.
//
// niche and tier are read out of the persisted breakdown, so the read stays
// entirely within this module's own table rather than joining the influencer
// module's data.
func (r *PostgresRepository) CorpusObservations(ctx context.Context) ([]engine.CorpusObservation, error) {
	const q = `
		SELECT DISTINCT ON (s.influencer_id)
		       s.influencer_id,
		       s.breakdown->>'niche' AS niche,
		       s.breakdown->>'tier'  AS tier,
		       (s.breakdown->>'observed_engagement_rate')::double precision AS er,
		       s.verification_tier
		FROM score s
		WHERE s.influencer_id IS NOT NULL
		  AND s.verification_tier = $1
		  AND s.breakdown->>'observed_engagement_rate' IS NOT NULL
		  AND s.breakdown->>'niche' IS NOT NULL
		  AND s.breakdown->>'tier'  IS NOT NULL
		ORDER BY s.influencer_id, s.created_at DESC`

	rows, err := r.pool.Query(ctx, q, contract.VerificationVerified)
	if err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "scoring.query_corpus", "could not read corpus observations")
	}
	defer rows.Close()

	out := make([]engine.CorpusObservation, 0)
	for rows.Next() {
		var o engine.CorpusObservation
		if err := rows.Scan(&o.InfluencerID, &o.Niche, &o.Tier, &o.EngagementRate, &o.VerificationTier); err != nil {
			return nil, errs.Wrap(err, errs.KindInternal, "scoring.scan_corpus", "could not read corpus observations")
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrap(err, errs.KindUnavailable, "scoring.rows_corpus", "could not read corpus observations")
	}
	return out, nil
}

// PublishCorpusBenchmark inserts a new corpus benchmark version and activates it.
func (r *PostgresRepository) PublishCorpusBenchmark(ctx context.Context, niche, tier string, b engine.Benchmark) error {
	return db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var nextVersion int
		if err := tx.QueryRow(ctx,
			`SELECT coalesce(max(version), 0) + 1 FROM benchmark WHERE niche = $1 AND tier = $2 AND metric = $3`,
			niche, tier, b.Metric,
		).Scan(&nextVersion); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "scoring.corpus_version", "could not determine next benchmark version")
		}

		if _, err := tx.Exec(ctx,
			`UPDATE benchmark SET active = false WHERE niche = $1 AND tier = $2 AND metric = $3 AND active`,
			niche, tier, b.Metric,
		); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "scoring.corpus_deactivate", "could not deactivate prior benchmark")
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO benchmark
				(niche, tier, metric, version, source, p10, p25, p50, p75, p90, mean, stddev, sample_size, active)
				VALUES ($1, $2, $3, $4, 'corpus', $5, $6, $7, $8, $9, $10, $11, $12, true)`,
			niche, tier, b.Metric, nextVersion,
			b.P10, b.P25, b.P50, b.P75, b.P90, b.Mean, b.Stddev, b.SampleSize,
		); err != nil {
			return errs.Wrap(err, errs.KindUnavailable, "scoring.corpus_insert", "could not publish corpus benchmark")
		}
		return nil
	})
}
