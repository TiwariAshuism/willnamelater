// Package model holds the waitlist module's domain types: the capture surfaces,
// the public request shape, and the persisted capture row.
package model

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Source names the funnel surface an email was captured on. The set is closed
// (migration 000034 mirrors it as a CHECK constraint): a capture from an unknown
// surface is rejected.
type Source string

const (
	// SourceConnectWall is the B3 connect-wall "return later" capture: a visitor
	// who reached the OAuth wall but was not ready to connect.
	SourceConnectWall Source = "connect_wall"
	// SourceMediaKit is the F1 media-kit waitlist: a creator asking to be told when
	// the media-kit surface ships.
	SourceMediaKit Source = "mediakit"
)

// Valid reports whether s is one of the recognised capture surfaces.
func (s Source) Valid() bool {
	switch s {
	case SourceConnectWall, SourceMediaKit:
		return true
	default:
		return false
	}
}

// CaptureRequest is the body of the public POST /waitlist endpoint.
type CaptureRequest struct {
	Email        string          `json:"email"`
	Source       string          `json:"source"`
	InfluencerID string          `json:"influencer_id,omitempty"`
	Props        json.RawMessage `json:"props,omitempty"`
}

// Capture is a validated, normalized email capture ready to persist. Email is
// already trimmed and lowercased so the (email, source) uniqueness is
// case-insensitive.
type Capture struct {
	Email        string
	Source       Source
	InfluencerID *uuid.UUID
	Props        []byte
}
