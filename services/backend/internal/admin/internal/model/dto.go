package model

import "time"

// FileDisputeRequest is the POST /audits/:id/disputes body. The audit under
// dispute travels in the path; the caller filing it travels on the request
// context, so the body carries only the reason.
type FileDisputeRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// ResolveDisputeRequest is the POST /admin/disputes/:id/resolve body. Decision
// is the admin's ruling (upheld or rejected); Notes is an optional free-text
// justification stored alongside the derived resolution.
type ResolveDisputeRequest struct {
	Decision string `json:"decision" binding:"required"`
	Notes    string `json:"notes,omitempty"`
}

// DisputeResponse is the dispute projection returned by the file, queue, and
// resolve routes. The nil-able actor and timestamp fields are omitted when unset
// so a client never sees an all-zero uuid or time.
type DisputeResponse struct {
	ID         string     `json:"id"`
	AuditJobID string     `json:"audit_job_id"`
	RaisedBy   string     `json:"raised_by,omitempty"`
	Reason     string     `json:"reason"`
	Status     string     `json:"status"`
	Resolution string     `json:"resolution,omitempty"`
	ResolvedBy string     `json:"resolved_by,omitempty"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CostDashboardResponse is the API-cost dashboard: the aggregate of every LLM
// generation, in total and broken down by model. The micros fields are the raw
// stored cost (millionths of a US dollar); the USD and rate fields are computed
// for display so the frontend renders no arithmetic.
type CostDashboardResponse struct {
	TotalGenerations  int            `json:"total_generations"`
	TotalInputTokens  int64          `json:"total_input_tokens"`
	TotalOutputTokens int64          `json:"total_output_tokens"`
	TotalCostMicros   int64          `json:"total_cost_micros"`
	TotalCostUSD      float64        `json:"total_cost_usd"`
	CachedGenerations int            `json:"cached_generations"`
	CacheHitRate      float64        `json:"cache_hit_rate"`
	ByModel           []CostResponse `json:"by_model"`
}

// CostResponse is one model's slice of the cost dashboard.
type CostResponse struct {
	Model             string  `json:"model"`
	Generations       int     `json:"generations"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostMicros        int64   `json:"cost_micros"`
	CostUSD           float64 `json:"cost_usd"`
	CachedGenerations int     `json:"cached_generations"`
}

// QueueSnapshot is one asynq queue's live state, as surfaced by the job monitor.
// The counts are a point-in-time view of the queue's task backlog and today's
// processed/failed counters.
type QueueSnapshot struct {
	Queue     string `json:"queue"`
	Size      int    `json:"size"`
	Pending   int    `json:"pending"`
	Active    int    `json:"active"`
	Scheduled int    `json:"scheduled"`
	Retry     int    `json:"retry"`
	Archived  int    `json:"archived"`
	Completed int    `json:"completed"`
	Processed int    `json:"processed"`
	Failed    int    `json:"failed"`
	Paused    bool   `json:"paused"`
	LatencyMs int64  `json:"latency_ms"`
}

// QueueMonitorResponse is the job monitor's view of every asynq queue.
type QueueMonitorResponse struct {
	Queues []QueueSnapshot `json:"queues"`
}

// FraudFeatures is a disputed audit's stored fraud estimate, projected as the
// feature vector services/ml/training consumes. It mirrors the fraud_result
// columns; it is a copy of what the audit run recorded, never a recomputation.
type FraudFeatures struct {
	Present                  bool    `json:"present"`
	FakeFollowerRate         float64 `json:"fake_follower_rate"`
	BotCommentRate           float64 `json:"bot_comment_rate"`
	EngagementAnomaly        float64 `json:"engagement_anomaly"`
	CliqueCount              int     `json:"clique_count"`
	CliqueMembershipFraction float64 `json:"clique_membership_fraction"`
	Confidence               float64 `json:"confidence"`
	ModelVersion             string  `json:"model_version"`
}

// TrainingLabel is one labelled example the dispute-review loop produces for the
// supervised fraud model. Label is the ground-truth target — true when the
// account was confirmed fraudulent/coordinated (dispute rejected), false when
// confirmed legitimate (dispute upheld). HasFeatures is false when the disputed
// audit never produced a stored fraud estimate; the example then carries a label
// with no features rather than a fabricated all-zero vector, and the trainer
// decides whether to keep it.
type TrainingLabel struct {
	DisputeID   string        `json:"dispute_id"`
	AuditJobID  string        `json:"audit_job_id"`
	Label       bool          `json:"label"`
	HasFeatures bool          `json:"has_features"`
	Features    FraudFeatures `json:"features"`
	ResolvedAt  time.Time     `json:"resolved_at"`
}

// LabelExportResponse is the GET /admin/training/labels payload: every decided
// dispute as a labelled training example, in a shape services/ml/training reads
// directly. Count is the number of examples.
type LabelExportResponse struct {
	Count  int             `json:"count"`
	Labels []TrainingLabel `json:"labels"`
}
