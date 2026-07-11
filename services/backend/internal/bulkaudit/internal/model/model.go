// Package model holds the bulkaudit module's request and response DTOs. They
// define the module's HTTP contract shape; the service that populates them is a
// scaffold returning errs.ErrNotImplemented until the batch orchestrator is built.
package model

import "time"

// CreateBulkAuditRequest is the body of POST /bulk-audits: a batch of handles on
// one platform to audit in a single job.
type CreateBulkAuditRequest struct {
	Platform string   `json:"platform" binding:"required"`
	Handles  []string `json:"handles" binding:"required,min=1"`
}

// BulkAuditResponse is the status of one batch job. Total is how many handles the
// batch covers; Completed is how many have finished, so a caller can render
// progress without listing every child audit.
type BulkAuditResponse struct {
	ID        string    `json:"id"`
	Platform  string    `json:"platform"`
	Status    string    `json:"status"`
	Total     int       `json:"total"`
	Completed int       `json:"completed"`
	CreatedAt time.Time `json:"created_at"`
}
