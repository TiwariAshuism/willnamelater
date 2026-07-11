// Package model holds the admin module's domain types (the dispute row and its
// state) and the request/response DTOs its HTTP surface exchanges. The domain
// types are what the repository reads and writes; the DTOs are what the handler
// binds and renders. The mapper folds one into the other.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Status is a dispute's lifecycle state. It mirrors the CHECK constraint on
// dispute.status (migration 000013): a dispute is filed open, and a review
// decision moves it to resolved (the flag was overturned) or rejected (the flag
// stands). under_review is reserved by the schema for a future triage step and
// is not produced by the current flow.
type Status string

const (
	// StatusOpen is a newly filed dispute awaiting admin review. It is the only
	// state the review queue lists.
	StatusOpen Status = "open"
	// StatusUnderReview is reserved by the schema for a triage step the current
	// flow does not use.
	StatusUnderReview Status = "under_review"
	// StatusResolved records that the dispute was upheld: the audit's fraud flag
	// was a false positive and the account is confirmed legitimate.
	StatusResolved Status = "resolved"
	// StatusRejected records that the dispute was denied: the audit's fraud flag
	// stands and the account is confirmed fraudulent/coordinated.
	StatusRejected Status = "rejected"
)

// Decision is the admin's ruling when resolving a dispute. It is the labelling
// act of the ML loop: the decision becomes both the dispute's terminal status
// and the supervised label the training export carries.
type Decision string

const (
	// DecisionUpheld rules the dispute valid — the audit's fraud flag was a false
	// positive — moving the dispute to StatusResolved and labelling the account
	// legitimate (a negative training example).
	DecisionUpheld Decision = "upheld"
	// DecisionRejected rules the dispute invalid — the audit's fraud flag stands —
	// moving the dispute to StatusRejected and labelling the account fraudulent (a
	// positive training example).
	DecisionRejected Decision = "rejected"
)

// Valid reports whether d is one of the two recognised decisions.
func (d Decision) Valid() bool {
	return d == DecisionUpheld || d == DecisionRejected
}

// Status returns the terminal dispute status a decision produces.
func (d Decision) Status() Status {
	if d == DecisionRejected {
		return StatusRejected
	}
	return StatusResolved
}

// FraudLabel returns the supervised training target the decision implies: true
// when the account was confirmed fraudulent/coordinated (the flag stood), false
// when it was confirmed legitimate (the flag was overturned).
func (d Decision) FraudLabel() bool {
	return d == DecisionRejected
}

// Dispute is one row of the dispute table. RaisedBy and ResolvedBy are uuid.Nil
// when the referenced account was deleted (the schema keeps the audit trail with
// ON DELETE SET NULL) or, for ResolvedBy, when the dispute is not yet resolved.
type Dispute struct {
	ID         uuid.UUID
	AuditJobID uuid.UUID
	RaisedBy   uuid.UUID
	Reason     string
	Status     Status
	Resolution string
	ResolvedBy uuid.UUID
	ResolvedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CreateDisputeParams is the input to filing a dispute.
type CreateDisputeParams struct {
	AuditJobID uuid.UUID
	RaisedBy   uuid.UUID
	Reason     string
}

// ResolveDisputeParams is the input to resolving a dispute. Status and
// Resolution are derived from the admin's Decision before the repository writes
// them, so the data layer stores only the resolved shape.
type ResolveDisputeParams struct {
	ID         uuid.UUID
	Status     Status
	Resolution string
	ResolvedBy uuid.UUID
}
