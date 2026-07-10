package model

import "time"

// SubmitAuditRequest is the POST /audits body. IdempotencyKey deduplicates
// retried submissions: two requests carrying the same key create at most one
// job. InfluencerID names the subject whose connected platforms are audited.
type SubmitAuditRequest struct {
	InfluencerID   string `json:"influencer_id" binding:"required,uuid"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	// RequestedPlatforms optionally restricts the audit to a subset of the
	// influencer's connected platforms. Empty means every connected platform.
	RequestedPlatforms []string `json:"requested_platforms,omitempty"`
}

// AuditResponse is the audit_job projection returned by all three routes. It
// reports the job's status and per-platform results; the score, fraud result,
// and narrative are served by the scoring, ml, and report modules keyed on the
// same audit id, so they are deliberately absent here.
type AuditResponse struct {
	ID                 string                   `json:"id"`
	Status             string                   `json:"status"`
	InfluencerID       string                   `json:"influencer_id,omitempty"`
	RequestedPlatforms []string                 `json:"requested_platforms"`
	Platforms          []PlatformResultResponse `json:"platforms"`
	ErrorCode          string                   `json:"error_code,omitempty"`
	ErrorMessage       string                   `json:"error_message,omitempty"`
	RequestedAt        time.Time                `json:"requested_at"`
	StartedAt          *time.Time               `json:"started_at,omitempty"`
	FinishedAt         *time.Time               `json:"finished_at,omitempty"`
}

// PlatformResultResponse is one platform's outcome within an audit.
type PlatformResultResponse struct {
	Platform     string     `json:"platform"`
	Status       string     `json:"status"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	FetchedAt    *time.Time `json:"fetched_at,omitempty"`
}
