package app

import "github.com/hibiken/asynq"

// RegisterTasks mounts every module's background task handlers onto the worker's
// mux. It is the task-side counterpart of Router: the one place that knows which
// modules exist, keeping modules free of any dependency on the worker.
//
// Handlers are registered here as each owning module lands. The audit
// orchestrator owns the audit:run task; its handler drives the fetch → score →
// fraud → report pipeline the worker consumes.
func (a *App) RegisterTasks(mux *asynq.ServeMux) {
	a.Modules.Audit.RegisterTasks(mux)
}
