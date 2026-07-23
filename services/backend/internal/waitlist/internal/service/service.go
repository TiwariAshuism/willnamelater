// Package service holds the waitlist module's business logic: validating and
// normalizing an email capture, and recording it idempotently on (email, source).
package service

import (
	"context"
	"encoding/json"
	"net/mail"
	"strings"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/model"
)

// Repository persists email captures. It is declared here (by its consumer) and
// satisfied by the repository package. Upsert is idempotent on (email, source).
type Repository interface {
	Upsert(ctx context.Context, c model.Capture) error
}

// Service records waitlist captures over the module's Repository.
type Service struct {
	repo Repository
}

// New wires the service over repo.
func New(repo Repository) *Service {
	return &Service{repo: repo}
}

// Capture validates and records one email capture. The email is trimmed,
// lowercased, and checked to be a bare, well-formed address; the source must be a
// recognised surface. The write is idempotent on (email, source): a repeat
// submission on the same surface records nothing new and still succeeds, so the
// caller can retry safely and a double-click is harmless.
func (s *Service) Capture(ctx context.Context, req model.CaptureRequest) error {
	email, err := normalizeEmail(req.Email)
	if err != nil {
		return err
	}

	source := model.Source(strings.TrimSpace(req.Source))
	if !source.Valid() {
		return errs.New(errs.KindInvalid, "waitlist.invalid_source",
			"source must be one of the recognised capture surfaces")
	}

	return s.repo.Upsert(ctx, model.Capture{
		Email:        email,
		Source:       source,
		InfluencerID: parseOptUUID(req.InfluencerID),
		Props:        validJSON(req.Props),
	})
}

// normalizeEmail trims and lowercases the address and rejects anything that is not
// a bare, well-formed email. A display-name form ("Name <a@b>") is rejected: the
// captured value must be the address alone.
func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", errs.New(errs.KindInvalid, "waitlist.email_required", "an email address is required")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", errs.New(errs.KindInvalid, "waitlist.email_invalid", "that is not a valid email address")
	}
	return email, nil
}

// parseOptUUID returns a pointer to the parsed uuid, or nil when the string is
// empty or unparseable — an optional, untrusted association must not fail the
// capture.
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

// validJSON returns raw only when it is well-formed JSON, dropping a malformed
// props blob to NULL rather than failing the capture.
func validJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	return raw
}
