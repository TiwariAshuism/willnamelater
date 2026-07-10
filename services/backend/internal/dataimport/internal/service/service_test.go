package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/dataimport/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

type fakeStore struct {
	gotUser, gotInf uuid.UUID
	gotPlatform     connector.Platform
	gotDataset      model.Dataset
	id              uuid.UUID
	err             error
}

func (f *fakeStore) Insert(_ context.Context, userID, influencerID uuid.UUID, p connector.Platform, ds model.Dataset) (uuid.UUID, error) {
	f.gotUser, f.gotInf, f.gotPlatform, f.gotDataset = userID, influencerID, p, ds
	return f.id, f.err
}

type fakeIdentity struct {
	id  uuid.UUID
	err error
}

func (f fakeIdentity) CallerID(context.Context) (uuid.UUID, error) { return f.id, f.err }

func fixedNow() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }

const csvBody = "post_id,published_at,likes,comments\n" +
	"p1,2026-07-01,100,10\n" +
	"p2,2026-07-02,200,20\n"

func newSvc(store *fakeStore, id fakeIdentity) *Service {
	s := New(store, id)
	s.now = fixedNow
	return s
}

func TestImportNormalizesUpload(t *testing.T) {
	caller := uuid.New()
	inf := uuid.New()
	store := &fakeStore{id: uuid.New()}
	svc := newSvc(store, fakeIdentity{id: caller})

	resp, err := svc.ImportInstagramCSV(context.Background(), model.ImportRequest{
		InfluencerID: inf.String(),
		Handle:       "  creator.ig  ",
		Followers:    5000,
		PostsCSV:     csvBody,
	})
	if err != nil {
		t.Fatalf("ImportInstagramCSV: %v", err)
	}

	if resp.Posts != 2 || resp.Platform != "instagram" || resp.Handle != "creator.ig" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if store.gotUser != caller {
		t.Error("upload was not stored against the authenticated caller")
	}
	if store.gotInf != inf || store.gotPlatform != connector.PlatformInstagram {
		t.Errorf("wrong influencer/platform stored: %v %v", store.gotInf, store.gotPlatform)
	}

	ds := store.gotDataset
	if ds.Handle != "creator.ig" {
		t.Errorf("handle not trimmed: %q", ds.Handle)
	}
	if ds.Source != sourceInstagramCSV {
		t.Errorf("source = %q, want %q", ds.Source, sourceInstagramCSV)
	}
	if len(ds.Posts) != 2 || ds.Posts[0].ID != "p1" || ds.Posts[0].Likes != 100 {
		t.Errorf("posts not parsed into the dataset: %+v", ds.Posts)
	}
	// One follower metric point at the pinned capture time; no fabricated series.
	if len(ds.Metrics) != 1 || ds.Metrics[0].Name != "followers" || ds.Metrics[0].Value != 5000 {
		t.Errorf("follower metric point wrong: %+v", ds.Metrics)
	}
	if !ds.CapturedAt.Equal(fixedNow()) {
		t.Errorf("captured_at = %v, want pinned now", ds.CapturedAt)
	}
	// A CSV upload has no per-comment data; the coordination signal must not be
	// fabricated.
	if len(ds.Comments) != 0 {
		t.Errorf("CSV upload must store no comments, got %d", len(ds.Comments))
	}
}

func TestImportRejectsUnauthenticated(t *testing.T) {
	svc := newSvc(&fakeStore{}, fakeIdentity{err: errs.New(errs.KindUnauthorized, "app.unauthenticated", "no")})
	_, err := svc.ImportInstagramCSV(context.Background(), model.ImportRequest{
		InfluencerID: uuid.New().String(), Handle: "h", PostsCSV: csvBody,
	})
	if err == nil || errs.KindOf(err) != errs.KindUnauthorized {
		t.Fatalf("want unauthorized, got %v", err)
	}
}

func TestImportRejectsBadInfluencerID(t *testing.T) {
	svc := newSvc(&fakeStore{}, fakeIdentity{id: uuid.New()})
	_, err := svc.ImportInstagramCSV(context.Background(), model.ImportRequest{
		InfluencerID: "not-a-uuid", Handle: "h", PostsCSV: csvBody,
	})
	if err == nil || errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid, got %v", err)
	}
}

func TestImportPropagatesParseError(t *testing.T) {
	svc := newSvc(&fakeStore{}, fakeIdentity{id: uuid.New()})
	_, err := svc.ImportInstagramCSV(context.Background(), model.ImportRequest{
		InfluencerID: uuid.New().String(),
		Handle:       "h",
		// Missing the required `likes` column.
		PostsCSV: "post_id,published_at,comments\np1,2026-07-01,10\n",
	})
	if err == nil || errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid parse error, got %v", err)
	}
}
