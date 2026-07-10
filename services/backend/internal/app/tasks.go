package app

import "github.com/hibiken/asynq"

// RegisterTasks mounts every module's background task handlers onto the worker's
// mux. It is the task-side counterpart of Router: the one place that knows which
// modules exist, keeping modules free of any dependency on the worker.
//
// Handlers are registered here as each owning module lands. Until then the mux
// is empty, which is the correct state: nothing enqueues, so nothing consumes.
func (a *App) RegisterTasks(_ *asynq.ServeMux) {}
