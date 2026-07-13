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

	reportID     uuid.UUID
	reportFound  bool
	revokedAudit uuid.UUID
	revokedUser  uuid.UUID
	granted      ShareGrant
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

func (f *fakeRepo) ReportIDOf(_ context.Context, _ uuid.UUID) (uuid.UUID, bool, error) {
	return f.reportID, f.reportFound, nil
}

func (f *fakeRepo) RevokeByAuditJob(_ context.Context, auditJobID uuid.UUID, _ time.Time) error {
	f.revokedAudit = auditJobID
	return nil
}

func (f *fakeRepo) InsertShareGrant(_ context.Context, g ShareGrant) (uuid.UUID, error) {
	f.granted = g
	return uuid.MustParse("33333333-3333-3333-3333-333333333333"), nil
}

func (f *fakeRepo) RevokeGrantsByUser(_ context.Context, userID uuid.UUID, _ time.Time) (int64, error) {
	f.revokedUser = userID
	return 1, nil
}

// fakeCaller is the authenticated caller. ownerID() is the creator who connected
// the account; any other id is somebody else (a brand, say) who must not be able
// to publish or share that creator's Graph data.
type fakeCaller struct {
	id  uuid.UUID
	err error
}

func (f fakeCaller) CallerID(context.Context) (uuid.UUID, error) { return f.id, f.err }

// fakeOwner reports who connected the audited account. A nil owner is an
// unclaimed profile — nobody who could direct a disclosure.
type fakeOwner struct {
	owner *uuid.UUID
	err   error
}

func (f fakeOwner) ConnectedOwnerOf(context.Context, uuid.UUID) (port.ConnectedOwner, error) {
	return port.ConnectedOwner{OwnerUserID: f.owner}, f.err
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

// f64p / intp are observed fraud measurements. Every fraud figure is a pointer:
// nil means the signal was never observed, and the report must disclose that
// rather than fold it into a 0.
func f64p(v float64) *float64 { return &v }
func intp(v int) *int         { return &v }

// ptr supplies a real pointer: Authenticity is nil when the dimension rested on no
// measurement, so a test asserting a value must say so explicitly.
func ptr[T any](v T) *T { return &v }

func infID() uuid.UUID { return uuid.MustParse("11111111-1111-1111-1111-111111111111") }
func audID() uuid.UUID { return uuid.MustParse("22222222-2222-2222-2222-222222222222") }

// ownerID is the creator who connected the audited account — the only person who
// may publish or share a report built from their Instagram Graph data.
func ownerID() uuid.UUID { return uuid.MustParse("44444444-4444-4444-4444-444444444444") }

// strangerID is somebody else (a brand that merely requested the audit). They may
// read their own audit, but must never disclose the creator's Graph data.
func strangerID() uuid.UUID { return uuid.MustParse("55555555-5555-5555-5555-555555555555") }

// ownedByCreator is the default ownership fake: the audited account is connected
// and owned by ownerID().
func ownedByCreator() fakeOwner {
	id := ownerID()
	return fakeOwner{owner: &id}
}

// newSvc builds a Service with default (no-op) repo and storage fakes, for the
// Assemble/PDF tests that do not exercise the publish path.
func newSvc(audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, fraud port.FraudReader, pdf port.PDFRenderer) *Service {
	return New(audit, score, narrative, fraud, pdf, &fakeRepo{}, &fakeStorage{},
		fakeCaller{id: ownerID()}, ownedByCreator(), nil, nil)
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
		fakeScore{view: port.ScoreView{Present: true, Overall: 82, Authenticity: ptr(74.0), Niche: "beauty",
			Subscores: []port.Subscore{{Name: "reach", Value: 80, Confidence: 0.6}}}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s", GrowthTips: []string{"t"},
			WeaknessFixPairs: []port.WeaknessFix{{Weakness: "w", Fix: "f"}}}},
		// EXPECTATION CHANGED: the fraud view carries RiskScore (the honest composite)
		// instead of FakeFollowerRate/BotCommentRate, and its measurements are pointers.
		fakeFraud{view: port.FraudView{Found: true, Present: true, RiskScore: f64p(63.5),
			CliqueCount: intp(7), CliqueMembershipFraction: f64p(0.42), Confidence: 0.6,
			ModelVersion: "clique-v1"}},
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
	if !rep.Fraud.Available || rep.Fraud.ModelVersion != "clique-v1" {
		t.Errorf("fraud headline not folded in: %+v", rep.Fraud)
	}
	if rep.Fraud.CliqueCount == nil || *rep.Fraud.CliqueCount != 7 {
		t.Errorf("clique count not folded in: %v", rep.Fraud.CliqueCount)
	}
	if rep.Fraud.RiskScore == nil || *rep.Fraud.RiskScore != 63.5 {
		t.Errorf("risk score not folded in: %v", rep.Fraud.RiskScore)
	}
	// A clique count was produced, so the commenter graph WAS built.
	if !rep.Fraud.CoordinationAnalyzed {
		t.Error("a stored clique count means coordination was analyzed")
	}
	if rep.InfluencerID != infID().String() || rep.Status != "succeeded" {
		t.Errorf("audit fields not mapped: %+v", rep)
	}
}

// The usual case: the ml pass produced a risk estimate but the snapshots carried
// no comments, so no commenter graph could be built. The assembled report must
// mark coordination as NOT analyzed and leave the clique figures nil — never fold
// an unobserved signal into a 0 that reads as "no coordination found".
func TestAssembleMarksCoordinationUnanalyzedWithoutComments(t *testing.T) {
	svc := newSvc(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s"}},
		fakeFraud{view: port.FraudView{Found: true, Present: true, RiskScore: f64p(41),
			Confidence: 0.25, ModelVersion: "risk-v2"}},
		&fakePDF{},
	)

	rep, err := svc.Assemble(context.Background(), audID().String())
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !rep.Fraud.Available {
		t.Fatal("a fraud pass that produced a risk score is still a fraud headline")
	}
	if rep.Fraud.CoordinationAnalyzed {
		t.Error("no clique count was stored, so coordination cannot be reported as analyzed")
	}
	if rep.Fraud.CliqueCount != nil || rep.Fraud.CliqueMembershipFraction != nil {
		t.Errorf("unobserved clique signals must stay nil, got %v / %v",
			rep.Fraud.CliqueCount, rep.Fraud.CliqueMembershipFraction)
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
		fakeScore{view: port.ScoreView{Present: true, Overall: 82, Authenticity: ptr(74.0), Niche: "beauty", Tier: "micro", BenchmarkLabel: "industry-bootstrap v1"}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s"}},
		fakeFraud{},
		&fakePDF{out: []byte("%PDF-1.4 body")},
		repo, store,
		fakeCaller{id: ownerID()}, ownedByCreator(), nil, nil,
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
		fakeCaller{id: ownerID()}, ownedByCreator(), nil, nil,
	)
	if _, err := svc.Publish(context.Background(), audID().String()); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid publishing a scoreless audit, got %v", err)
	}
}

func TestPublicBadgeReturnsSnapshotAndShareLink(t *testing.T) {
	repo := &fakeRepo{found: true, get: PublishedReport{
		StorageKey: "reports/x.pdf",
		Badge:      BadgeSnapshot{Overall: 90, Authenticity: ptr(80.0), Niche: "fitness", Tier: "mid"},
	}}
	svc := New(fakeAudit{}, fakeScore{}, fakeNarrative{}, fakeFraud{}, &fakePDF{}, repo,
		&fakeStorage{shareURL: "https://s3/x?signed"}, fakeCaller{id: ownerID()}, ownedByCreator(), nil, nil)

	badge, err := svc.PublicBadge(context.Background(), "some-slug")
	if err != nil {
		t.Fatalf("PublicBadge: %v", err)
	}
	if badge.Overall != 90 || badge.Niche != "fitness" || badge.PDFURL != "https://s3/x?signed" {
		t.Fatalf("badge projection wrong: %+v", badge)
	}
}

// --- Creator-directed sharing (Meta Platform Terms §3.c/§3.d) --------------

// newOwnedSvc builds a Service whose audited account is connected and owned by
// ownerID(), with caller as the authenticated user.
func newOwnedSvc(repo *fakeRepo, caller uuid.UUID, owner fakeOwner) *Service {
	return New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 80}},
		fakeNarrative{},
		fakeFraud{},
		&fakePDF{out: []byte("%PDF")},
		repo, &fakeStorage{},
		fakeCaller{id: caller}, owner, nil, nil,
	)
}

// THE gate. A brand may request an audit of a creator it does not own — the audit
// read is scoped to the requester and succeeds. But publishing that report
// discloses the CREATOR's Instagram Graph data, and Meta Platform Terms §3.c
// permits disclosure only on the direction of the user whose data it is. So a
// requester who is not the connected-account owner must be refused.
func TestPublishRefusesANonOwner(t *testing.T) {
	repo := &fakeRepo{}
	svc := newOwnedSvc(repo, strangerID(), ownedByCreator())

	_, err := svc.Publish(context.Background(), audID().String())
	if errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("want forbidden publishing someone else's connected account, got %v", err)
	}
	if repo.upserted.PublicSlug != "" {
		t.Fatal("nothing may be published for a non-owner")
	}
}

// An unclaimed profile (nobody connected it) has no user who could direct a
// disclosure, so its report cannot be published at all.
func TestPublishRefusesAnUnclaimedProfile(t *testing.T) {
	repo := &fakeRepo{}
	svc := newOwnedSvc(repo, ownerID(), fakeOwner{owner: nil})

	if _, err := svc.Publish(context.Background(), audID().String()); errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("want forbidden publishing an unclaimed profile, got %v", err)
	}
}

// A published certificate must expire: §3.d forbids retaining Platform Data once
// retention is no longer necessary, and a stale reach figure is not a credential.
func TestPublishSetsAnExpiry(t *testing.T) {
	repo := &fakeRepo{slug: "s"}
	svc := newOwnedSvc(repo, ownerID(), ownedByCreator())

	if _, err := svc.Publish(context.Background(), audID().String()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if repo.upserted.ExpiresAt.IsZero() {
		t.Fatal("a published badge must carry an expiry, never live forever")
	}
	if got := repo.upserted.ExpiresAt.Sub(repo.upserted.GeneratedAt); got != badgeTTL {
		t.Fatalf("expiry = generated+%v, want generated+%v", got, badgeTTL)
	}
}

// Sharing records the creator's express direction: a NAMED recipient and a STATED
// purpose, time-bounded. Both fields are the §3.c requirement, not decoration.
func TestShareRecordsTheCreatorsDirection(t *testing.T) {
	repo := &fakeRepo{reportFound: true, reportID: uuid.MustParse("66666666-6666-6666-6666-666666666666")}
	svc := newOwnedSvc(repo, ownerID(), ownedByCreator())

	res, err := svc.Share(context.Background(), audID().String(), "  Acme Cosmetics  ", " Q3 campaign vetting ")
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if repo.granted.Recipient != "Acme Cosmetics" || repo.granted.Purpose != "Q3 campaign vetting" {
		t.Fatalf("recipient/purpose not recorded (and trimmed): %+v", repo.granted)
	}
	if repo.granted.GrantedByUserID != ownerID() {
		t.Fatalf("the grant must record the creator who directed it: %v", repo.granted.GrantedByUserID)
	}
	if repo.granted.ExpiresAt.Sub(repo.granted.GrantedAt) != grantTTL {
		t.Fatal("a share grant must be time-bounded, never perpetual")
	}
	if res.GrantID == "" || res.Recipient != "Acme Cosmetics" {
		t.Fatalf("share receipt wrong: %+v", res)
	}
}

// An unnamed recipient or an unstated purpose is not a direction we can act on:
// §3.c authorizes sharing only "for the purposes as specified in the User's
// direction". Both must be rejected before anything is recorded.
func TestShareRequiresRecipientAndPurpose(t *testing.T) {
	tests := []struct{ name, recipient, purpose string }{
		{"no recipient", "   ", "vetting"},
		{"no purpose", "Acme", "  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{reportFound: true}
			svc := newOwnedSvc(repo, ownerID(), ownedByCreator())

			if _, err := svc.Share(context.Background(), audID().String(), tt.recipient, tt.purpose); errs.KindOf(err) != errs.KindInvalid {
				t.Fatalf("want invalid, got %v", err)
			}
			if repo.granted.Recipient != "" {
				t.Fatal("nothing may be recorded for an incomplete direction")
			}
		})
	}
}

// A non-owner cannot share the creator's data either — same gate as publish.
func TestShareRefusesANonOwner(t *testing.T) {
	repo := &fakeRepo{reportFound: true}
	svc := newOwnedSvc(repo, strangerID(), ownedByCreator())

	if _, err := svc.Share(context.Background(), audID().String(), "Acme", "vetting"); errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
	if repo.granted.Recipient != "" {
		t.Fatal("a non-owner must not record a share grant")
	}
}

// Sharing an unpublished report is refused: there is nothing to disclose yet.
func TestShareRequiresAPublishedReport(t *testing.T) {
	repo := &fakeRepo{reportFound: false}
	svc := newOwnedSvc(repo, ownerID(), ownedByCreator())

	if _, err := svc.Share(context.Background(), audID().String(), "Acme", "vetting"); errs.KindOf(err) != errs.KindInvalid {
		t.Fatalf("want invalid sharing an unpublished report, got %v", err)
	}
}

// The creator can withdraw a published report at any time (§3.d "delete promptly
// on request"); a non-owner cannot.
func TestRevokeIsOwnerOnly(t *testing.T) {
	repo := &fakeRepo{}
	if err := newOwnedSvc(repo, ownerID(), ownedByCreator()).Revoke(context.Background(), audID().String()); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if repo.revokedAudit != audID() {
		t.Fatal("the owner's revocation must reach the repository")
	}

	other := &fakeRepo{}
	if err := newOwnedSvc(other, strangerID(), ownedByCreator()).Revoke(context.Background(), audID().String()); errs.KindOf(err) != errs.KindForbidden {
		t.Fatalf("want forbidden revoking someone else's report, got %v", err)
	}
	if other.revokedAudit != uuid.Nil {
		t.Fatal("a non-owner must not revoke")
	}
}

func TestPublicBadgeUnknownSlugIsNotFound(t *testing.T) {
	svc := New(fakeAudit{}, fakeScore{}, fakeNarrative{}, fakeFraud{}, &fakePDF{}, &fakeRepo{found: false},
		&fakeStorage{}, fakeCaller{id: ownerID()}, ownedByCreator(), nil, nil)
	if _, err := svc.PublicBadge(context.Background(), "nope"); errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("want not-found for an unknown slug, got %v", err)
	}
}

// --- notification on publish ------------------------------------------------

// fakeMailer records what was sent, and can fail on demand. A relay that is down
// is the normal case this must survive, not an exceptional one.
type fakeMailer struct {
	sent []port.Message
	err  error
}

func (m *fakeMailer) Send(_ context.Context, msg port.Message) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

type fakeRecipient struct {
	email string
	err   error
}

func (r fakeRecipient) EmailOf(_ context.Context, _ uuid.UUID) (string, error) {
	return r.email, r.err
}

// publishWith builds a service whose publish path succeeds — a scored audit owned
// by the caller — so the only variable under test is the notification.
func publishWith(mailer port.Mailer, rcpt port.Recipient) *Service {
	return New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82, Niche: "beauty", Tier: "micro"}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s"}},
		fakeFraud{},
		&fakePDF{out: []byte("%PDF-1.4 body")},
		&fakeRepo{slug: "durable-slug"}, &fakeStorage{},
		fakeCaller{id: ownerID()}, ownedByCreator(),
		mailer, rcpt,
	)
}

func TestPublishNotifiesTheCreator(t *testing.T) {
	mailer := &fakeMailer{}
	svc := publishWith(mailer, fakeRecipient{email: "creator@example.com"})

	res, err := svc.Publish(context.Background(), audID().String())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(mailer.sent))
	}
	msg := mailer.sent[0]
	if got := msg.To; len(got) != 1 || got[0] != "creator@example.com" {
		t.Errorf("To = %v, want [creator@example.com]", got)
	}
	// The mail is worthless without the link it exists to deliver.
	if !strings.Contains(msg.Text, res.PDFURL) {
		t.Errorf("message body does not carry the share URL %q:\n%s", res.PDFURL, msg.Text)
	}
	if msg.Subject == "" {
		t.Error("message has no subject")
	}
}

// THE contract. The report is stored, the badge row is durable, and the link is
// minted — the publish has succeeded. A mail relay having a bad minute must not
// turn that into a reported failure, which would invite the caller to retry an
// operation that already took effect.
func TestPublishSucceedsWhenTheRelayIsDown(t *testing.T) {
	cases := map[string]*Service{
		"relay refuses the message": publishWith(
			&fakeMailer{err: errs.New(errs.KindUnavailable, "email.dial", "relay is down")},
			fakeRecipient{email: "creator@example.com"},
		),
		"recipient cannot be resolved": publishWith(
			&fakeMailer{},
			fakeRecipient{err: errs.New(errs.KindNotFound, "auth.no_user", "no such user")},
		),
		"recipient has no address": publishWith(
			&fakeMailer{},
			fakeRecipient{email: ""},
		),
		// A developer machine with no SMTP relay configured at all.
		"no mailer configured": publishWith(nil, nil),
	}

	for name, svc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := svc.Publish(context.Background(), audID().String())
			if err != nil {
				t.Fatalf("Publish failed because of a notification problem: %v", err)
			}
			if res.PublicSlug == "" {
				t.Error("publish returned no slug; the report was not actually published")
			}
		})
	}
}
