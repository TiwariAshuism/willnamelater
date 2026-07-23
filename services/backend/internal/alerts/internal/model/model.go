// Package model holds the alerts module's request and response DTOs. They define
// the module's HTTP contract shape; the service that populates them is a scaffold
// returning errs.ErrNotImplemented until the alerting engine is built.
package model

import "time"

// Comparator names how an alert rule compares a metric against its threshold.
type Comparator string

// The comparators an alert rule may use.
const (
	// ComparatorBelow fires when the watched metric falls below the threshold.
	ComparatorBelow Comparator = "below"
	// ComparatorAbove fires when the watched metric rises above the threshold.
	ComparatorAbove Comparator = "above"
)

// CreateAlertRequest is the body of POST /alerts: a rule watching one metric of
// one influencer and notifying the caller when it crosses a threshold.
type CreateAlertRequest struct {
	InfluencerID string     `json:"influencer_id" binding:"required"`
	Metric       string     `json:"metric" binding:"required"`
	Comparator   Comparator `json:"comparator" binding:"required"`
	Threshold    float64    `json:"threshold"`
}

// AlertResponse is one configured alert rule.
type AlertResponse struct {
	ID           string     `json:"id"`
	InfluencerID string     `json:"influencer_id"`
	Metric       string     `json:"metric"`
	Comparator   Comparator `json:"comparator"`
	Threshold    float64    `json:"threshold"`
	CreatedAt    time.Time  `json:"created_at"`
}
