package llm

import (
	"context"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// CostSummary is the aggregate of every llm_generation row, in total and broken
// down by model. CostMicros is millionths of a US dollar. It is the read behind
// the admin API-cost dashboard; the admin module reaches it through a port so it
// never imports llm.
type CostSummary struct {
	TotalGenerations  int
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalCostMicros   int64
	CachedGenerations int
	ByModel           []ModelCost
}

// ModelCost is one model's slice of the aggregate.
type ModelCost struct {
	Model             string
	Generations       int
	InputTokens       int64
	OutputTokens      int64
	CostMicros        int64
	CachedGenerations int
}

// CostSummary aggregates llm_generation into a per-model and total cost view.
// An empty table yields a zero summary, not an error, so a fresh deployment's
// dashboard renders cleanly rather than failing.
func (m *Module) CostSummary(ctx context.Context) (CostSummary, error) {
	const q = `SELECT model,
			count(*),
			coalesce(sum(input_tokens), 0),
			coalesce(sum(output_tokens), 0),
			coalesce(sum(cost_micros), 0),
			coalesce(sum(CASE WHEN cached THEN 1 ELSE 0 END), 0)
		FROM llm_generation
		GROUP BY model
		ORDER BY model`

	rows, err := m.pool.Query(ctx, q)
	if err != nil {
		return CostSummary{}, errs.Wrap(err, errs.KindUnavailable,
			"llm.cost_summary", "could not aggregate generation cost")
	}
	defer rows.Close()

	var summary CostSummary
	for rows.Next() {
		var (
			mc          ModelCost
			generations int64
			cached      int64
		)
		if err := rows.Scan(&mc.Model, &generations, &mc.InputTokens, &mc.OutputTokens, &mc.CostMicros, &cached); err != nil {
			return CostSummary{}, errs.Wrap(err, errs.KindUnavailable,
				"llm.cost_summary", "could not read generation cost")
		}
		mc.Generations = int(generations)
		mc.CachedGenerations = int(cached)
		summary.ByModel = append(summary.ByModel, mc)

		summary.TotalGenerations += mc.Generations
		summary.TotalInputTokens += mc.InputTokens
		summary.TotalOutputTokens += mc.OutputTokens
		summary.TotalCostMicros += mc.CostMicros
		summary.CachedGenerations += mc.CachedGenerations
	}
	if err := rows.Err(); err != nil {
		return CostSummary{}, errs.Wrap(err, errs.KindUnavailable,
			"llm.cost_summary", "could not aggregate generation cost")
	}
	return summary, nil
}
