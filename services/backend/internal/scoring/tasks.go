package scoring

import (
	"context"
	"log/slog"

	"github.com/hibiken/asynq"
)

// TaskRecomputeCorpus is the asynq task type the nightly scheduler enqueues to
// republish corpus-derived benchmarks. It is stable: the scheduler persists it
// in Redis, so renaming it would orphan the schedule entry.
const TaskRecomputeCorpus = "scoring:recompute-corpus"

// RegisterTasks registers the corpus-recompute handler on mux. It is the
// task-side counterpart of RegisterRoutes: the worker's composition root calls
// it so the module owns the task type it consumes. The task carries no payload —
// it republishes every corpus cell that has reached the sample threshold.
func (m *Module) RegisterTasks(mux *asynq.ServeMux) {
	mux.HandleFunc(TaskRecomputeCorpus, m.processRecomputeCorpus)
}

// processRecomputeCorpus runs one corpus recompute. A failure returns an error
// so asynq retries the run; the recompute is idempotent (it only republishes
// cells already at the sample threshold), so a retry is safe.
func (m *Module) processRecomputeCorpus(ctx context.Context, _ *asynq.Task) error {
	n, err := m.service.RecomputeCorpus(ctx)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "scoring corpus recomputed", slog.Int("cells_republished", n))
	return nil
}
