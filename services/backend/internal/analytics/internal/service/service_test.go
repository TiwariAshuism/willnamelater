package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/getnyx/influaudit/backend/internal/analytics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// fakeRepo records the last inserted event and can fail on demand.
type fakeRepo struct {
	inserted []model.Event
	counts   map[string]int64
	err      error
}

func (f *fakeRepo) Insert(_ context.Context, ev model.Event) error {
	if f.err != nil {
		return f.err
	}
	f.inserted = append(f.inserted, ev)
	return nil
}

func (f *fakeRepo) CountByType(context.Context) (map[string]int64, error) {
	return f.counts, f.err
}

func TestIngestRejectsUnknownEventType(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(repo)
	err := svc.Ingest(context.Background(), model.IngestRequest{EventType: "not_a_real_event"}, model.IngestMeta{})
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for an unknown event type, got %v", err)
	}
	if len(repo.inserted) != 0 {
		t.Fatal("nothing may be recorded for an invalid event type")
	}
}

// The privacy invariants at the one place events flow through: the User-Agent is
// hashed (never stored raw), no owner is invented on the unauthenticated ingest,
// and an unparseable client id is dropped to NULL rather than failing the event.
func TestIngestHashesUAAndDropsUntrustedIdentity(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(repo)

	err := svc.Ingest(context.Background(), model.IngestRequest{
		EventType:    string(model.EventLandingView),
		InfluencerID: "not-a-uuid",
		SessionID:    "sess-123",
		Props:        json.RawMessage(`{"variant":"b"}`),
	}, model.IngestMeta{UserAgent: "Mozilla/5.0", Referrer: "https://ref.example"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(repo.inserted) != 1 {
		t.Fatalf("want one event recorded, got %d", len(repo.inserted))
	}
	ev := repo.inserted[0]
	if ev.InfluencerID != nil {
		t.Error("an unparseable influencer id must be dropped to nil, not stored")
	}
	if ev.IsOwner != nil {
		t.Error("the public ingest cannot know ownership; is_owner must stay nil")
	}
	if ev.UserAgentHash == nil || *ev.UserAgentHash == "Mozilla/5.0" {
		t.Errorf("the User-Agent must be hashed, never stored raw: %v", ev.UserAgentHash)
	}
	if len(*ev.UserAgentHash) != 64 {
		t.Errorf("want a hex sha-256 (64 chars), got %d", len(*ev.UserAgentHash))
	}
	if ev.SessionID == nil || *ev.SessionID != "sess-123" {
		t.Errorf("session id not recorded: %v", ev.SessionID)
	}
	if string(ev.Props) != `{"variant":"b"}` {
		t.Errorf("well-formed props must be kept: %s", ev.Props)
	}
}

// Malformed props are dropped to NULL rather than failing the insert.
func TestIngestDropsMalformedProps(t *testing.T) {
	repo := &fakeRepo{}
	if err := New(repo).Ingest(context.Background(), model.IngestRequest{
		EventType: string(model.EventScoreShown),
		Props:     json.RawMessage(`{not json`),
	}, model.IngestMeta{}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if repo.inserted[0].Props != nil {
		t.Error("malformed props must be dropped to nil")
	}
}

// RecordShareOpen writes a share_open attributed to owner/external, and refuses an
// empty slug (there would be nothing to attribute the open to).
func TestRecordShareOpen(t *testing.T) {
	repo := &fakeRepo{}
	svc := New(repo)

	if err := svc.RecordShareOpen(context.Background(), "  slug-1  ", false); err != nil {
		t.Fatalf("RecordShareOpen: %v", err)
	}
	ev := repo.inserted[0]
	if ev.EventType != model.EventShareOpen || ev.PublicSlug == nil || *ev.PublicSlug != "slug-1" {
		t.Fatalf("share open not recorded correctly: %+v", ev)
	}
	if ev.IsOwner == nil || *ev.IsOwner {
		t.Fatalf("an external open must be recorded is_owner=false, got %v", ev.IsOwner)
	}

	if err := svc.RecordShareOpen(context.Background(), "   ", false); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid for an empty slug, got %v", err)
	}
}
