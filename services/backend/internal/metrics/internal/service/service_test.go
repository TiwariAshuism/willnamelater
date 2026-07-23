package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/metrics/internal/model"
	"github.com/getnyx/influaudit/backend/internal/platform/crypto"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// --- fakes ---------------------------------------------------------------

type fakeReadRepo struct {
	metricsResp  model.MetricSeriesResponse
	metricsErr   error
	postsResp    []model.PostResponse
	postsErr     error
	summaryResp  model.ProfileSummaryResponse
	summaryErr   error
	seriesResp   []model.FollowerPoint
	seriesErr    error
	gotMetricsID string
	gotPostsID   string
	gotSummaryID string
	gotSeriesID  uuid.UUID
	metricsCalls int
	postsCalls   int
	summaryCalls int
	seriesCalls  int
}

func (f *fakeReadRepo) GetInfluencerMetrics(_ context.Context, id string, _ model.MetricSeriesRequest) (model.MetricSeriesResponse, error) {
	f.metricsCalls++
	f.gotMetricsID = id
	return f.metricsResp, f.metricsErr
}

func (f *fakeReadRepo) ListInfluencerPosts(_ context.Context, id string, _ model.ListPostsRequest) ([]model.PostResponse, error) {
	f.postsCalls++
	f.gotPostsID = id
	return f.postsResp, f.postsErr
}

func (f *fakeReadRepo) GetInfluencerProfileSummary(_ context.Context, id string) (model.ProfileSummaryResponse, error) {
	f.summaryCalls++
	f.gotSummaryID = id
	return f.summaryResp, f.summaryErr
}

func (f *fakeReadRepo) FollowerSeries(_ context.Context, influencerID uuid.UUID) ([]model.FollowerPoint, error) {
	f.seriesCalls++
	f.gotSeriesID = influencerID
	return f.seriesResp, f.seriesErr
}

type fakeIngestRepo struct {
	posts     []model.PostRow
	points    []model.MetricPointRow
	comments  []model.CommentRow
	audience  []model.AudienceDemographicRow
	audCalled bool

	postIDs   map[string]uuid.UUID
	upsertErr error
	pointsErr error
	commErr   error
	audErr    error
}

func (f *fakeIngestRepo) UpsertPosts(_ context.Context, _ pgx.Tx, rows []model.PostRow) (map[string]uuid.UUID, error) {
	f.posts = rows
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if f.postIDs != nil {
		return f.postIDs, nil
	}
	m := make(map[string]uuid.UUID, len(rows))
	for _, p := range rows {
		m[p.PlatformPostID] = uuid.New()
	}
	return m, nil
}

func (f *fakeIngestRepo) InsertMetricPoints(_ context.Context, _ pgx.Tx, rows []model.MetricPointRow) error {
	f.points = rows
	return f.pointsErr
}

func (f *fakeIngestRepo) InsertComments(_ context.Context, _ pgx.Tx, rows []model.CommentRow) error {
	f.comments = rows
	return f.commErr
}

func (f *fakeIngestRepo) InsertAudienceDemographics(_ context.Context, _ pgx.Tx, rows []model.AudienceDemographicRow) error {
	f.audCalled = true
	f.audience = rows
	return f.audErr
}

// fakeTx and fakeBeginner let InTx run without a database.
type fakeTx struct {
	pgx.Tx
	commits   int
	rollbacks int
}

func (t *fakeTx) Commit(context.Context) error   { t.commits++; return nil }
func (t *fakeTx) Rollback(context.Context) error { t.rollbacks++; return nil }

type fakeBeginner struct{ tx *fakeTx }

func (b *fakeBeginner) Begin(context.Context) (pgx.Tx, error) { return b.tx, nil }

// testCipher builds a usable cipher from a fixed key; the value is irrelevant to
// these tests beyond being a valid 32-byte key.
func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	key := bytes.Repeat([]byte{0x2a}, crypto.KeySize)
	c, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func newTestService(t *testing.T, read *fakeReadRepo, ingest *fakeIngestRepo, store *fakeSaltStore) (*Service, *fakeTx) {
	t.Helper()
	tx := &fakeTx{}
	salt := NewSaltProvider(store, testCipher(t))
	return New(&fakeBeginner{tx: tx}, read, ingest, salt), tx
}

// --- read paths ----------------------------------------------------------

func TestGetInfluencerMetrics(t *testing.T) {
	valid := uuid.NewString()
	want := model.MetricSeriesResponse{InfluencerID: valid}

	tests := []struct {
		name       string
		id         string
		repoErr    error
		wantKind   errs.Kind
		wantErr    bool
		wantCalled bool
	}{
		{name: "valid id delegates", id: valid, wantCalled: true},
		{name: "malformed id is invalid", id: "not-a-uuid", wantErr: true, wantKind: errs.KindInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			read := &fakeReadRepo{metricsResp: want, metricsErr: tt.repoErr}
			svc, _ := newTestService(t, read, &fakeIngestRepo{}, &fakeSaltStore{})

			got, err := svc.GetInfluencerMetrics(context.Background(), tt.id, model.MetricSeriesRequest{})
			if tt.wantErr {
				if errs.KindOf(err) != tt.wantKind {
					t.Fatalf("kind = %v, want %v (err %v)", errs.KindOf(err), tt.wantKind, err)
				}
				if read.metricsCalls != 0 {
					t.Fatalf("repository called for an invalid id")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.InfluencerID != want.InfluencerID {
				t.Fatalf("response = %+v, want %+v", got, want)
			}
			if tt.wantCalled && read.gotMetricsID != tt.id {
				t.Fatalf("repository got id %q, want %q", read.gotMetricsID, tt.id)
			}
		})
	}
}

func TestListInfluencerPosts(t *testing.T) {
	valid := uuid.NewString()

	t.Run("malformed id is invalid", func(t *testing.T) {
		read := &fakeReadRepo{}
		svc, _ := newTestService(t, read, &fakeIngestRepo{}, &fakeSaltStore{})
		if _, err := svc.ListInfluencerPosts(context.Background(), "bad", model.ListPostsRequest{}); errs.KindOf(err) != errs.KindInvalid {
			t.Fatalf("kind = %v, want Invalid", errs.KindOf(err))
		}
		if read.postsCalls != 0 {
			t.Fatalf("repository called for an invalid id")
		}
	})

	t.Run("valid id delegates", func(t *testing.T) {
		read := &fakeReadRepo{postsResp: []model.PostResponse{{ID: "p1"}}}
		svc, _ := newTestService(t, read, &fakeIngestRepo{}, &fakeSaltStore{})
		got, err := svc.ListInfluencerPosts(context.Background(), valid, model.ListPostsRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].ID != "p1" {
			t.Fatalf("posts = %+v, want one post p1", got)
		}
	})
}

func TestGetInfluencerProfileSummary(t *testing.T) {
	valid := uuid.NewString()

	t.Run("malformed id is invalid and never reaches the repo", func(t *testing.T) {
		read := &fakeReadRepo{}
		svc, _ := newTestService(t, read, &fakeIngestRepo{}, &fakeSaltStore{})
		if _, err := svc.GetInfluencerProfileSummary(context.Background(), "nope"); errs.KindOf(err) != errs.KindInvalid {
			t.Fatalf("kind = %v, want Invalid", errs.KindOf(err))
		}
		if read.summaryCalls != 0 {
			t.Fatalf("repository called for an invalid id")
		}
	})

	t.Run("valid id delegates", func(t *testing.T) {
		read := &fakeReadRepo{summaryResp: model.ProfileSummaryResponse{InfluencerID: valid}}
		svc, _ := newTestService(t, read, &fakeIngestRepo{}, &fakeSaltStore{})
		got, err := svc.GetInfluencerProfileSummary(context.Background(), valid)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.InfluencerID != valid || read.gotSummaryID != valid {
			t.Fatalf("summary = %+v, gotID = %q", got, read.gotSummaryID)
		}
	})
}

func TestInstagramFollowerSeries(t *testing.T) {
	id := uuid.New()
	want := []model.FollowerPoint{
		{At: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Followers: 100},
		{At: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), Followers: 110},
	}
	read := &fakeReadRepo{seriesResp: want}
	svc, _ := newTestService(t, read, &fakeIngestRepo{}, &fakeSaltStore{})

	got, err := svc.InstagramFollowerSeries(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[1].Followers != 110 || read.gotSeriesID != id {
		t.Fatalf("series = %+v, gotID = %v", got, read.gotSeriesID)
	}
}

// --- ingest --------------------------------------------------------------

func TestIngestValidatesIDs(t *testing.T) {
	svc, _ := newTestService(t, &fakeReadRepo{}, &fakeIngestRepo{}, &fakeSaltStore{})

	if err := svc.Ingest(context.Background(), uuid.Nil, uuid.New(), connector.Snapshot{}); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("nil influencer: kind = %v, want Invalid", errs.KindOf(err))
	}
	if err := svc.Ingest(context.Background(), uuid.New(), uuid.Nil, connector.Snapshot{}); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("nil audit job: kind = %v, want Invalid", errs.KindOf(err))
	}
}

func TestIngestPersistsAndPseudonymizes(t *testing.T) {
	influencerID := uuid.New()
	auditJobID := uuid.New()
	const rawAuthor = "yt-channel-SECRET-000111"
	const otherAuthor = "yt-channel-OTHER-222333"

	postAID := uuid.New()
	postBID := uuid.New()

	snap := connector.Snapshot{
		Platform: connector.PlatformYouTube,
		Metrics: []connector.MetricPoint{
			{At: time.Now().Add(-time.Hour), Name: "subscribers", Value: 1000},
			{At: time.Now(), Name: "subscribers", Value: 1010},
		},
		Posts: []connector.Post{
			{ID: "postA", URL: "https://x/postA", PublishedAt: time.Now().Add(-48 * time.Hour), Caption: "a", MediaType: "IMAGE", Likes: 5, Comments: 2},
			{ID: "postB", Likes: 0}, // zero counters, no URL/caption/time/media-type -> NULLs
		},
		Comments: []connector.Comment{
			{PostID: "postA", AuthorID: rawAuthor, Text: "nice", At: time.Now()},
			{PostID: "postB", AuthorID: rawAuthor, Text: "again"}, // same author, different post
			{PostID: "postB", AuthorID: otherAuthor, Text: "different"},
		},
	}

	ingest := &fakeIngestRepo{postIDs: map[string]uuid.UUID{"postA": postAID, "postB": postBID}}
	svc, tx := newTestService(t, &fakeReadRepo{}, ingest, &fakeSaltStore{})

	if err := svc.Ingest(context.Background(), influencerID, auditJobID, snap); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if tx.commits != 1 {
		t.Fatalf("commits = %d, want 1", tx.commits)
	}

	// Posts: mapped column-for-column, NULLs preserved for the bare post.
	if len(ingest.posts) != 2 {
		t.Fatalf("posts = %d, want 2", len(ingest.posts))
	}
	postA := ingest.posts[0]
	if postA.PlatformPostID != "postA" || postA.InfluencerID != influencerID || postA.AuditJobID != auditJobID {
		t.Fatalf("postA identity wrong: %+v", postA)
	}
	if postA.Permalink == nil || *postA.Permalink != "https://x/postA" || postA.Caption == nil || postA.PostedAt == nil {
		t.Fatalf("postA should carry permalink/caption/posted_at: %+v", postA)
	}
	if postA.MediaType == nil || *postA.MediaType != "IMAGE" {
		t.Fatalf("postA should carry its media type: %+v", postA)
	}
	postB := ingest.posts[1]
	if postB.Permalink != nil || postB.Caption != nil || postB.PostedAt != nil || postB.MediaType != nil {
		t.Fatalf("postB absent fields must be nil (NULL), got %+v", postB)
	}

	// Metric points passed through.
	if len(ingest.points) != 2 {
		t.Fatalf("points = %d, want 2", len(ingest.points))
	}
	for _, p := range ingest.points {
		if p.InfluencerID != influencerID || p.Platform != "youtube" || p.AuditJobID != auditJobID {
			t.Fatalf("metric point identity wrong: %+v", p)
		}
	}

	// Comments: hashed, resolved to post uuids, raw id never present.
	if len(ingest.comments) != 3 {
		t.Fatalf("comments = %d, want 3", len(ingest.comments))
	}

	var hashA1, hashA2, hashOther []byte
	for _, c := range ingest.comments {
		if len(c.AuthorHash) != 32 {
			t.Fatalf("author hash length = %d, want 32", len(c.AuthorHash))
		}
		if bytes.Contains(c.AuthorHash, []byte(rawAuthor)) || bytes.Contains(c.AuthorHash, []byte(otherAuthor)) {
			t.Fatalf("author hash contains the raw author id: %x", c.AuthorHash)
		}
		switch c.PostID {
		case postAID:
			hashA1 = c.AuthorHash
		case postBID:
			if c.Body != nil && *c.Body == "again" {
				hashA2 = c.AuthorHash
			} else {
				hashOther = c.AuthorHash
			}
		default:
			t.Fatalf("comment resolved to unexpected post id %v", c.PostID)
		}
	}

	// HMAC stability: the same author on two different posts hashes identically.
	if !bytes.Equal(hashA1, hashA2) {
		t.Fatalf("same author hashed differently across posts: %x vs %x", hashA1, hashA2)
	}
	// A different author yields a different hash.
	if bytes.Equal(hashA1, hashOther) {
		t.Fatalf("different authors collided to the same hash: %x", hashA1)
	}
}

// TestIngestPersistsAudienceDemographics covers the demographics ingest: an
// observed distribution is flattened one-row-per-bucket, tagged with the audit and
// platform, and only observed buckets are written.
func TestIngestPersistsAudienceDemographics(t *testing.T) {
	influencerID, auditJobID := uuid.New(), uuid.New()
	snap := connector.Snapshot{
		Platform:   connector.PlatformInstagram,
		CapturedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Posts:      []connector.Post{{ID: "p1", Likes: 10}},
		Audience: &connector.AudienceBreakdown{
			Countries: map[string]float64{"US": 0.6, "GB": 0.2},
			AgeGroups: map[string]float64{"18-24": 0.5},
			Gender:    map[string]float64{"female": 0.7, "male": 0.3},
		},
	}
	ingest := &fakeIngestRepo{}
	svc, _ := newTestService(t, &fakeReadRepo{}, ingest, &fakeSaltStore{})
	if err := svc.Ingest(context.Background(), influencerID, auditJobID, snap); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// 2 countries + 1 age + 2 gender = 5 observed buckets.
	if len(ingest.audience) != 5 {
		t.Fatalf("audience rows = %d, want 5 (only observed buckets)", len(ingest.audience))
	}
	dims := map[string]int{}
	for _, r := range ingest.audience {
		if r.InfluencerID != influencerID || r.AuditJobID != auditJobID || r.Platform != "instagram" {
			t.Fatalf("audience row identity wrong: %+v", r)
		}
		if r.Fraction <= 0 || r.CapturedAt != snap.CapturedAt {
			t.Fatalf("audience row value wrong: %+v", r)
		}
		dims[r.Dimension]++
	}
	if dims["country"] != 2 || dims["age"] != 1 || dims["gender"] != 2 {
		t.Fatalf("dimension counts = %v, want country:2 age:1 gender:2", dims)
	}
}

// TestIngestNilAudienceWritesNothing pins the honesty contract: a snapshot with no
// audience (YouTube, or a sub-100-follower account) writes zero demographic rows —
// absence is never a zero-filled bucket.
func TestIngestNilAudienceWritesNothing(t *testing.T) {
	ingest := &fakeIngestRepo{}
	svc, _ := newTestService(t, &fakeReadRepo{}, ingest, &fakeSaltStore{})
	snap := connector.Snapshot{Platform: connector.PlatformYouTube, Posts: []connector.Post{{ID: "p1"}}}
	if err := svc.Ingest(context.Background(), uuid.New(), uuid.New(), snap); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(ingest.audience) != 0 {
		t.Fatalf("nil audience wrote %d rows, want 0", len(ingest.audience))
	}
}

// TestIngestNeverHandsRawAuthorToRepository is the security-critical guarantee:
// scan every byte the repository was asked to write and prove the raw author id
// appears nowhere.
func TestIngestNeverHandsRawAuthorToRepository(t *testing.T) {
	const rawAuthor = "ig-user-RAW-IDENTIFIER-99887766"
	snap := connector.Snapshot{
		Platform: connector.PlatformInstagram,
		Posts:    []connector.Post{{ID: "p1"}},
		Comments: []connector.Comment{{PostID: "p1", AuthorID: rawAuthor, Text: "hello"}},
	}
	ingest := &fakeIngestRepo{}
	svc, _ := newTestService(t, &fakeReadRepo{}, ingest, &fakeSaltStore{})

	if err := svc.Ingest(context.Background(), uuid.New(), uuid.New(), snap); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	for _, c := range ingest.comments {
		if c.Body != nil && strings.Contains(*c.Body, rawAuthor) {
			continue // body is user text; not the identity we are protecting
		}
		if bytes.Contains(c.AuthorHash, []byte(rawAuthor)) {
			t.Fatalf("raw author id leaked into author hash")
		}
	}
}

func TestIngestOrphanComment(t *testing.T) {
	snap := connector.Snapshot{
		Platform: connector.PlatformYouTube,
		Posts:    []connector.Post{{ID: "postA"}},
		Comments: []connector.Comment{{PostID: "ghost", AuthorID: "a"}},
	}
	ingest := &fakeIngestRepo{postIDs: map[string]uuid.UUID{"postA": uuid.New()}}
	svc, tx := newTestService(t, &fakeReadRepo{}, ingest, &fakeSaltStore{})

	err := svc.Ingest(context.Background(), uuid.New(), uuid.New(), snap)
	if errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want Invalid", errs.KindOf(err))
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("orphan comment must roll back: commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestIngestPostMissingID(t *testing.T) {
	snap := connector.Snapshot{Platform: connector.PlatformYouTube, Posts: []connector.Post{{ID: ""}}}
	svc, _ := newTestService(t, &fakeReadRepo{}, &fakeIngestRepo{}, &fakeSaltStore{})
	if err := svc.Ingest(context.Background(), uuid.New(), uuid.New(), snap); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("kind = %v, want Invalid", errs.KindOf(err))
	}
}

func TestIngestPropagatesRepoError(t *testing.T) {
	boom := errors.New("db down")
	snap := connector.Snapshot{Platform: connector.PlatformYouTube, Posts: []connector.Post{{ID: "p1"}}}
	ingest := &fakeIngestRepo{upsertErr: boom}
	svc, tx := newTestService(t, &fakeReadRepo{}, ingest, &fakeSaltStore{})

	if err := svc.Ingest(context.Background(), uuid.New(), uuid.New(), snap); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want it to wrap %v", err, boom)
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("repo error must roll back: commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestIngestSaltFailurePropagates(t *testing.T) {
	snap := connector.Snapshot{
		Platform: connector.PlatformYouTube,
		Posts:    []connector.Post{{ID: "p1"}},
		Comments: []connector.Comment{{PostID: "p1", AuthorID: "a"}},
	}
	store := &fakeSaltStore{loadErr: errors.New("salt store down")}
	ingest := &fakeIngestRepo{}
	svc, tx := newTestService(t, &fakeReadRepo{}, ingest, store)

	if err := svc.Ingest(context.Background(), uuid.New(), uuid.New(), snap); err == nil {
		t.Fatal("want error when the salt cannot be loaded")
	}
	// Hashing happens before the transaction opens, so nothing was written.
	if tx.commits != 0 || tx.rollbacks != 0 {
		t.Fatalf("salt failure must not open a transaction: commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
	if ingest.posts != nil {
		t.Fatalf("no rows should have been written")
	}
}
