package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

type fakeAudit struct {
	view port.AuditView
	err  error
}

func (f fakeAudit) AuditView(context.Context, string) (port.AuditView, error) {
	return f.view, f.err
}

type fakeScore struct {
	view port.ScoreView
	err  error
}

func (f fakeScore) ScoreOf(context.Context, uuid.UUID) (port.ScoreView, error) {
	return f.view, f.err
}

type fakeNarrative struct {
	view port.Narrative
	err  error
}

func (f fakeNarrative) NarrativeOf(context.Context, uuid.UUID) (port.Narrative, error) {
	return f.view, f.err
}

type fakeFraud struct {
	view port.FraudView
	err  error
}

func (f fakeFraud) FraudOf(context.Context, uuid.UUID) (port.FraudView, error) {
	return f.view, f.err
}

type fakePDF struct {
	gotHTML []byte
	out     []byte
	err     error
}

func (f *fakePDF) RenderHTML(_ context.Context, html []byte) ([]byte, error) {
	f.gotHTML = html
	return f.out, f.err
}

type fakeRepo struct {
	upserted          ReportRecord
	slug              string
	get               PublishedReport
	found             bool
	upsertErr, getErr error
}

func (f *fakeRepo) UpsertReport(_ context.Context, rec ReportRecord) (string, error) {
	f.upserted = rec
	if f.upsertErr != nil {
		return "", f.upsertErr
	}
	if f.slug != "" {
		return f.slug, nil
	}
	return rec.PublicSlug, nil
}

func (f *fakeRepo) GetByPublicSlug(_ context.Context, _ string) (PublishedReport, bool, error) {
	return f.get, f.found, f.getErr
}

type fakeStorage struct {
	putKey, putType string
	putData         []byte
	shareURL        string
	putErr, urlErr  error
}

func (f *fakeStorage) Put(_ context.Context, key, contentType string, data []byte) error {
	f.putKey, f.putType, f.putData = key, contentType, data
	return f.putErr
}

func (f *fakeStorage) ShareURL(key string, _ time.Duration) (string, error) {
	if f.urlErr != nil {
		return "", f.urlErr
	}
	if f.shareURL != "" {
		return f.shareURL, nil
	}
	return "https://s3.example/" + key + "?signed", nil
}

func infID() uuid.UUID { return uuid.MustParse("11111111-1111-1111-1111-111111111111") }
func audID() uuid.UUID { return uuid.MustParse("22222222-2222-2222-2222-222222222222") }

// newSvc builds a Service with default (no-op) repo and storage fakes, for the
// Assemble/PDF tests that do not exercise the publish path.
func newSvc(audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, fraud port.FraudReader, pdf port.PDFRenderer) *Service {
	return New(audit, score, narrative, fraud, pdf, &fakeRepo{}, &fakeStorage{})
}

func fullView() port.AuditView {
	return port.AuditView{
		ID:           audID(),
		InfluencerID: infID(),
		Status:       "succeeded",
		Platforms:    []string{"youtube"},
	}
}

func TestAssembleFoldsAllSources(t *testing.T) {
	svc := newSvc(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82, Authenticity: 74, Niche: "beauty",
			Subscores: []port.Subscore{{Name: "reach", Value: 80, Confidence: 0.6}}}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s", GrowthTips: []string{"t"},
			WeaknessFixPairs: []port.WeaknessFix{{Weakness: "w", Fix: "f"}}}},
		fakeFraud{view: port.FraudView{Found: true, Present: true, CliqueCount: 7,
			CliqueMembershipFraction: 0.42, Confidence: 0.6, ModelVersion: "clique-v1"}},
		&fakePDF{},
	)

	rep, err := svc.Assemble(context.Background(), audID().String())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !rep.Score.Available || rep.Score.Overall != 82 || len(rep.Score.Subscores) != 1 {
		t.Errorf("score not folded in: %+v", rep.Score)
	}
	if !rep.NarrativeAvailable || rep.Narrative.Summary != "s" || len(rep.Narrative.WeaknessFixPairs) != 1 {
		t.Errorf("narrative not folded in: %+v", rep.Narrative)
	}
	if !rep.Fraud.Available || rep.Fraud.CliqueCount != 7 || rep.Fraud.ModelVersion != "clique-v1" {
		t.Errorf("fraud headline not folded in: %+v", rep.Fraud)
	}
	if rep.InfluencerID != infID().String() || rep.Status != "succeeded" {
		t.Errorf("audit fields not mapped: %+v", rep)
	}
}

// A failed audit has no score and no narrative; the report must still assemble,
// disclosing both as absent rather than erroring.
func TestAssembleToleratesAbsentScoreAndNarrative(t *testing.T) {
	svc := newSvc(
		fakeAudit{view: port.AuditView{ID: audID(), InfluencerID: infID(), Status: "failed"}},
		fakeScore{view: port.ScoreView{Present: false}},
		fakeNarrative{view: port.Narrative{Present: false}},
		fakeFraud{view: port.FraudView{Found: false}},
		&fakePDF{},
	)

	rep, err := svc.Assemble(context.Background(), audID().String())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if rep.Score.Available {
		t.Error("score must be absent for a failed audit")
	}
	if rep.NarrativeAvailable {
		t.Error("narrative must be absent when none was stored")
	}
	if rep.Fraud.Available {
		t.Error("fraud headline must be absent when no fraud pass was recorded")
	}
}

// An unauthorized/unknown audit surfaces from the AuditReader; the report must
// not read a score or narrative for it.
func TestAssemblePropagatesAuditError(t *testing.T) {
	svc := newSvc(
		fakeAudit{err: errs.New(errs.KindNotFound, "audit.not_found", "no such audit")},
		fakeScore{},
		fakeNarrative{},
		fakeFraud{},
		&fakePDF{},
	)
	_, err := svc.Assemble(context.Background(), "whatever")
	if err == nil || errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("want not-found from audit reader, got %v", err)
	}
}

func TestPDFRendersAssembledReport(t *testing.T) {
	pdf := &fakePDF{out: []byte("%PDF-1.4 fake")}
	svc := newSvc(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "hello-summary"}},
		fakeFraud{},
		pdf,
	)

	out, err := svc.PDF(context.Background(), audID().String())
	if err != nil {
		t.Fatalf("PDF: %v", err)
	}
	if string(out) != "%PDF-1.4 fake" {
		t.Errorf("PDF did not return the renderer's bytes, got %q", out)
	}
	// The HTML handed to the renderer must be the assembled report, not empty.
	if !strings.Contains(string(pdf.gotHTML), "hello-summary") {
		t.Error("renderer was not given the assembled report HTML")
	}
}

func TestPDFPropagatesRendererError(t *testing.T) {
	svc := newSvc(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true}},
		fakeNarrative{view: port.Narrative{Present: false}},
		fakeFraud{},
		&fakePDF{err: errors.New("gotenberg down")},
	)
	if _, err := svc.PDF(context.Background(), audID().String()); err == nil {
		t.Fatal("want error when the PDF renderer fails")
	}
}

func TestPublishStoresPDFAndPersistsBadge(t *testing.T) {
	repo := &fakeRepo{slug: "durable-slug"}
	store := &fakeStorage{}
	svc := New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82, Authenticity: 74, Niche: "beauty", Tier: "micro", BenchmarkLabel: "industry-bootstrap v1"}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s"}},
		fakeFraud{},
		&fakePDF{out: []byte("%PDF-1.4 body")},
		repo, store,
	)

	res, err := svc.Publish(context.Background(), audID().String())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// The PDF was uploaded under the audit-scoped key with the pdf content type.
	if store.putKey != "reports/"+audID().String()+".pdf" || store.putType != "application/pdf" || string(store.putData) != "%PDF-1.4 body" {
		t.Fatalf("PDF not stored correctly: key=%q type=%q", store.putKey, store.putType)
	}
	// The badge snapshot persisted the public-safe headline, and the checksum/size
	// were computed over the real bytes.
	if repo.upserted.Badge.Overall != 82 || repo.upserted.Badge.Niche != "beauty" || repo.upserted.SizeBytes != int64(len("%PDF-1.4 body")) || repo.upserted.Checksum == "" {
		t.Fatalf("badge/record not persisted: %+v", repo.upserted)
	}
	// The durable slug from the repo (not the freshly-minted candidate) is returned.
	if res.PublicSlug != "durable-slug" || res.BadgeURL != "/reports/durable-slug" || res.PDFURL == "" {
		t.Fatalf("publish result wrong: %+v", res)
	}
}

// A report with no score is not a shareable credential; publishing must refuse.
func TestPublishRejectsScorelessAudit(t *testing.T) {
	svc := New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: false}},
		fakeNarrative{view: port.Narrative{Present: false}},
		fakeFraud{},
		&fakePDF{out: []byte("x")},
		&fakeRepo{}, &fakeStorage{},
	)
	if _, err := svc.Publish(context.Background(), audID().String()); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid publishing a scoreless audit, got %v", err)
	}
}

func TestPublicBadgeReturnsSnapshotAndShareLink(t *testing.T) {
	repo := &fakeRepo{found: true, get: PublishedReport{
		StorageKey: "reports/x.pdf",
		Badge:      BadgeSnapshot{Overall: 90, Authenticity: 80, Niche: "fitness", Tier: "mid"},
	}}
	svc := New(fakeAudit{}, fakeScore{}, fakeNarrative{}, fakeFraud{}, &fakePDF{}, repo, &fakeStorage{shareURL: "https://s3/x?signed"})

	badge, err := svc.PublicBadge(context.Background(), "some-slug")
	if err != nil {
		t.Fatalf("PublicBadge: %v", err)
	}
	if badge.Overall != 90 || badge.Niche != "fitness" || badge.PDFURL != "https://s3/x?signed" {
		t.Fatalf("badge projection wrong: %+v", badge)
	}
}

func TestPublicBadgeUnknownSlugIsNotFound(t *testing.T) {
	svc := New(fakeAudit{}, fakeScore{}, fakeNarrative{}, fakeFraud{}, &fakePDF{}, &fakeRepo{found: false}, &fakeStorage{})
	if _, err := svc.PublicBadge(context.Background(), "nope"); errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("want not-found for an unknown slug, got %v", err)
	}
}
