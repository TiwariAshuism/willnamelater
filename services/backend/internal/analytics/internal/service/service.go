// Package service holds the analytics module's business logic: validating and
// recording first-party funnel events, and recording server-side share opens.
//
// It enforces the module's privacy invariants at the one place every event flows
// through: the User-Agent is hashed (never stored raw), no IP is ever accepted,
// client-claimed identity is recorded as untrusted signal (an unparseable id is
// dropped, never allowed to fail the ingest), and absence is stored as NULL.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/analytics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Repository persists analytics events and answers the aggregate read. It is
// declared here (by its consumer) and satisfied by the repository package.
type Repository interface {
	Insert(ctx context.Context, ev model.Event) error
	CountByType(ctx context.Context) (map[string]int64, error)
}

// Service records funnel events over the module's Repository.
type Service struct {
	repo Repository
}

// New wires the service over repo.
func New(repo Repository) *Service {
	return &Service{repo: repo}
}

// Ingest validates and records one first-party funnel event from the public
// endpoint. An unknown event_type is rejected. Everything else is best-effort
// signal: an unparseable influencer/audit id is dropped to NULL rather than
// failing the event, invalid props are dropped, the User-Agent is hashed, and
// is_owner is left NULL — the public ingest cannot prove ownership, and a guess
// would be a fabrication.
func (s *Service) Ingest(ctx context.Context, req model.IngestRequest, meta model.IngestMeta) error {
	et := model.EventType(strings.TrimSpace(req.EventType))
	if !et.Valid() {
		return errs.New(errs.KindInvalid, "analytics.invalid_event_type",
			"event_type is not one of the recognised funnel events")
	}

	ev := model.Event{
		EventType:     et,
		OccurredAt:    time.Now().UTC(),
		InfluencerID:  parseOptUUID(req.InfluencerID),
		AuditJobID:    parseOptUUID(req.AuditJobID),
		PublicSlug:    optString(req.PublicSlug),
		SessionID:     optString(req.SessionID),
		Referrer:      optString(meta.Referrer),
		UserAgentHash: hashUserAgent(meta.UserAgent),
		Props:         validJSON(req.Props),
		// is_owner is deliberately nil: the public ingest is unauthenticated, so
		// ownership is undeterminable here and must not be invented.
	}
	return s.repo.Insert(ctx, ev)
}

// RecordShareOpen records a public badge/handle page open, attributing it to the
// owner or an external visitor. This is the server-side path the report module
// calls (through its OpenRecorder port, adapted by the composition root) whenever
// a badge is READ — the origin of the external-share-open metric. The slug/handle
// the badge was reached by is trusted context here because the report module set
// it, not the client. isOwner is honestly false for every unauthenticated open.
func (s *Service) RecordShareOpen(ctx context.Context, publicSlug string, isOwner bool) error {
	slug := strings.TrimSpace(publicSlug)
	if slug == "" {
		return errs.New(errs.KindInvalid, "analytics.share_open_no_slug",
			"a share open must name the badge it refers to")
	}
	owner := isOwner
	return s.repo.Insert(ctx, model.Event{
		EventType:  model.EventShareOpen,
		OccurredAt: time.Now().UTC(),
		PublicSlug: &slug,
		IsOwner:    &owner,
	})
}

// Summary returns the raw per-type event counts. It backs the optional protected
// GET /events/summary; raw events remain the source of truth.
func (s *Service) Summary(ctx context.Context) (map[string]int64, error) {
	return s.repo.CountByType(ctx)
}

// parseOptUUID returns a pointer to the parsed uuid, or nil when the string is
// empty OR unparseable. A malformed client-supplied id is untrusted context that
// must not fail the whole event, so it is simply dropped to NULL.
func parseOptUUID(s string) *uuid.UUID {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

// optString returns a pointer to the trimmed string, or nil when it is empty, so
// an absent value is stored as SQL NULL rather than "".
func optString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// hashUserAgent returns the hex SHA-256 of the User-Agent, or nil when there is
// none. The raw User-Agent is never stored — only this one-way hash, kept for
// coarse de-duplication. No IP is involved anywhere.
func hashUserAgent(ua string) *string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(ua))
	h := hex.EncodeToString(sum[:])
	return &h
}

// validJSON returns raw only when it is well-formed JSON, so a malformed props
// blob is dropped to NULL rather than failing the insert or poisoning the column.
func validJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	return raw
}
