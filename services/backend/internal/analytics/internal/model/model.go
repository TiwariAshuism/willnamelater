// Package model holds the analytics module's domain types: the funnel event
// enumeration, the public ingest request shape, the server-computed context that
// accompanies it, and the persisted event row.
package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// EventType enumerates the funnel stages the analytics log records. The set is
// closed and documented (migration 000033 mirrors it as a CHECK constraint): an
// event whose type is not one of these is rejected at ingest, so the log stays an
// analyzable enumeration rather than a free-text sink.
type EventType string

const (
	// EventLandingView marks a visit to the public acquisition landing page.
	EventLandingView EventType = "landing_view"
	// EventConnectStart marks a visitor beginning the connect flow.
	EventConnectStart EventType = "connect_start"
	// EventPrerequisitePass marks a visitor clearing the pre-connect prerequisites.
	EventPrerequisitePass EventType = "prerequisite_pass"
	// EventOAuthGrant marks a completed OAuth grant.
	EventOAuthGrant EventType = "oauth_grant"
	// EventScoreShown marks the score being surfaced to the visitor.
	EventScoreShown EventType = "score_shown"
	// EventShareOpen marks a public badge/handle page being opened. It is the
	// funnel's PRIMARY success metric when the opener is external (non-owner), and
	// is the one event recorded server-side by the report module rather than by the
	// browser.
	EventShareOpen EventType = "share_open"
	// EventMediaKitCTAClick marks a click on the media-kit call to action.
	EventMediaKitCTAClick EventType = "mediakit_cta_click"
)

// Valid reports whether e is one of the documented funnel events.
func (e EventType) Valid() bool {
	switch e {
	case EventLandingView, EventConnectStart, EventPrerequisitePass,
		EventOAuthGrant, EventScoreShown, EventShareOpen, EventMediaKitCTAClick:
		return true
	default:
		return false
	}
}

// IngestRequest is the body of the public POST /events endpoint. Every field is
// optional except event_type. The identity-ish fields are UNTRUSTED context the
// browser supplies: they are recorded as signal, never as proof of who is calling.
type IngestRequest struct {
	EventType    string          `json:"event_type"`
	InfluencerID string          `json:"influencer_id,omitempty"`
	AuditJobID   string          `json:"audit_job_id,omitempty"`
	PublicSlug   string          `json:"public_slug,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	Props        json.RawMessage `json:"props,omitempty"`
}

// IngestMeta is the context the transport computes server-side and the client
// cannot forge into the record: the User-Agent (which the service hashes, never
// storing raw) and the referrer header. No IP is ever part of this.
type IngestMeta struct {
	UserAgent string
	Referrer  string
}

// SummaryResponse is the GET /events/summary body: the count of recorded events
// per event type. It is the aggregate read over the raw event log.
type SummaryResponse struct {
	Counts map[string]int64 `json:"counts"`
}

// Event is a single persisted analytics row. Every attribution field is a pointer
// so an absent value is stored as SQL NULL — the log records only what actually
// happened, never a fabricated zero or empty string.
type Event struct {
	EventType     EventType
	OccurredAt    time.Time
	InfluencerID  *uuid.UUID
	AuditJobID    *uuid.UUID
	PublicSlug    *string
	SessionID     *string
	IsOwner       *bool
	Referrer      *string
	UserAgentHash *string
	Props         []byte
}
