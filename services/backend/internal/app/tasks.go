package app

import (
	"github.com/hibiken/asynq"

	"github.com/getnyx/influaudit/backend/internal/scoring"
)

// RegisterTasks mounts every module's background task handlers onto the worker's
// mux. It is the task-side counterpart of Router: the one place that knows which
// modules exist, keeping modules free of any dependency on the worker.
//
// Handlers are registered here as each owning module lands. The audit
// orchestrator owns the audit:run task; its handler drives the fetch → score →
// fraud → report pipeline the worker consumes. The scoring module owns the
// nightly corpus-recompute task the scheduler enqueues.
func (a *App) RegisterTasks(mux *asynq.ServeMux) {
	a.Modules.Audit.RegisterTasks(mux)
	a.Modules.Scoring.RegisterTasks(mux)
}

// ScheduleEntry is one periodic task the worker's scheduler enqueues on a cron
// spec. The task type is owned by the module that handles it (registered in
// RegisterTasks); app knows only the cadence, keeping the whole schedule in one
// auditable place.
type ScheduleEntry struct {
	Cronspec    string
	Task        *asynq.Task
	Description string
}

// ScheduledTasks returns the periodic tasks the worker's scheduler enqueues. The
// corpus recompute runs nightly so any (niche, tier) benchmark cell that crossed
// the sample threshold during the day is republished from real percentiles
// rather than staying on the industry-bootstrap seed.
func (a *App) ScheduledTasks() []ScheduleEntry {
	return []ScheduleEntry{
		{
			Cronspec:    "0 3 * * *",
			Task:        asynq.NewTask(scoring.TaskRecomputeCorpus, nil),
			Description: "nightly corpus benchmark recompute",
		},
	}
}
