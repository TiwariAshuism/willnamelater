package service

import (
	"context"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/waitlist/internal/model"
)

type fakeRepo struct {
	upserted []model.Capture
	err      error
}

func (f *fakeRepo) Upsert(_ context.Context, c model.Capture) error {
	if f.err != nil {
		return f.err
	}
	f.upserted = append(f.upserted, c)
	return nil
}

// A valid capture is normalized (trimmed + lowercased) so the (email, source)
// uniqueness is case-insensitive, and recorded on a recognised surface.
func TestCaptureNormalizesAndRecords(t *testing.T) {
	repo := &fakeRepo{}
	err := New(repo).Capture(context.Background(), model.CaptureRequest{
		Email:  "  Creator@Example.COM ",
		Source: "connect_wall",
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(repo.upserted) != 1 {
		t.Fatalf("want one capture, got %d", len(repo.upserted))
	}
	c := repo.upserted[0]
	if c.Email != "creator@example.com" {
		t.Errorf("email not normalized: %q", c.Email)
	}
	if c.Source != model.SourceConnectWall {
		t.Errorf("source wrong: %q", c.Source)
	}
}

func TestCaptureRejectsBadEmail(t *testing.T) {
	for _, email := range []string{"", "   ", "not-an-email", "Name <a@b.com>", "a@b@c"} {
		repo := &fakeRepo{}
		if err := New(repo).Capture(context.Background(), model.CaptureRequest{Email: email, Source: "mediakit"}); errs.KindOf(err) != errs.KindInvalid {
			t.Fatalf("email %q: want invalid, got %v", email, err)
		}
		if len(repo.upserted) != 0 {
			t.Fatalf("email %q: nothing may be recorded", email)
		}
	}
}

func TestCaptureRejectsUnknownSource(t *testing.T) {
	repo := &fakeRepo{}
	if err := New(repo).Capture(context.Background(), model.CaptureRequest{Email: "a@b.com", Source: "somewhere_else"}); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for an unknown source, got %v", err)
	}
	if len(repo.upserted) != 0 {
		t.Fatal("nothing may be recorded for an unknown source")
	}
}
