// Package service assembles a finished audit's deliverable. It reads the audit
// (scoped to the caller), the score, and the narrative through the report
// module's ports, folds them into one canonical Report, and — for the PDF path —
// renders that Report to HTML and hands it to the PDF renderer.
//
// It never fabricates: a missing score or narrative is disclosed as absent in
// the assembled Report, not filled with a placeholder number or prose.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/report/internal/render"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

// shareTTL bounds a presigned PDF link. It is generous enough that a badge page
// can hand the link straight to a browser, and short enough that a leaked URL
// does not grant indefinite access to the stored PDF.
const shareTTL = 24 * time.Hour

// badgeTTL bounds how long a published badge stays live before it must be
// re-published. Meta Platform Terms §3.d requires Platform Data to be deleted
// once retention is no longer necessary and updated or deleted on request, so a
// certificate built from Instagram Graph data cannot be permanent. A bounded life
// is also the honest posture: a months-old reach figure proves nothing about the
// account today, and a stale certificate is exactly the misrepresentation a
// verified badge exists to prevent.
const badgeTTL = 90 * 24 * time.Hour

// grantTTL bounds a creator's share of a report with a named brand (§3.c). A
// direction to share is never perpetual; the creator re-grants if the brand still
// needs it.
const grantTTL = 30 * 24 * time.Hour

// BadgeSnapshot is the public-safe projection frozen at publish time and stored
// as badge_jsonb. It carries only non-sensitive headline fields — never the
// advisory narrative or anything identifying the account owner beyond the public
// handle the creator already publishes.
type BadgeSnapshot struct {
	Handle           string   `json:"handle"`
	Overall          float64  `json:"overall"`
	Authenticity     *float64 `json:"authenticity,omitempty"`
	Niche            string   `json:"niche"`
	Tier             string   `json:"tier"`
	BenchmarkLabel   string   `json:"benchmark_label"`
	VerificationTier string   `json:"verification_tier"`
	GeneratedAt      string   `json:"generated_at"`
}

// ReportRecord is a published report to persist.
type ReportRecord struct {
	AuditJobID  uuid.UUID
	StorageKey  string
	PublicSlug  string
	Badge       BadgeSnapshot
	SizeBytes   int64
	Checksum    string
	GeneratedAt time.Time
	ExpiresAt   time.Time
}

// PublishedReport is a stored report in the shape the public badge read needs.
type PublishedReport struct {
	StorageKey  string
	Badge       BadgeSnapshot
	GeneratedAt time.Time
}

// ShareGrant is one creator-directed share: the evidence that the account owner
// expressly directed us to disclose this report to a NAMED recipient for a STATED
// purpose (Meta Platform Terms §3.c). Both fields are required — an unnamed
// recipient or an unstated purpose is not a valid direction.
type ShareGrant struct {
	ReportID        uuid.UUID
	GrantedByUserID uuid.UUID
	Recipient       string
	Purpose         string
	GrantedAt       time.Time
	ExpiresAt       time.Time
}

// Repository persists and reads published reports. It is declared by the service
// (its consumer) and satisfied by the repository package. UpsertReport returns
// the durable public slug — the pre-existing one when a report is re-published,
// so a shared link is stable — which may differ from the candidate on rec.
//
// GetByPublicSlug returns only a LIVE report: one that is neither revoked nor
// past its expiry. A revoked or expired badge is indistinguishable from one that
// never existed, so withdrawing consent genuinely withdraws access.
type Repository interface {
	UpsertReport(ctx context.Context, rec ReportRecord) (publicSlug string, err error)
	GetByPublicSlug(ctx context.Context, slug string) (PublishedReport, bool, error)
	// GetByHandle returns the newest LIVE published report for an Instagram handle,
	// resolved through the handle frozen into the badge snapshot at publish time.
	// It is the durable /@handle alias over the opaque slug and applies the same
	// liveness filter as GetByPublicSlug: a revoked or expired badge is invisible.
	GetByHandle(ctx context.Context, handle string) (PublishedReport, bool, error)
	// ReportIDOf resolves the stored report row for an audit job.
	ReportIDOf(ctx context.Context, auditJobID uuid.UUID) (uuid.UUID, bool, error)
	// RevokeByAuditJob withdraws a published report: the slug stops resolving.
	RevokeByAuditJob(ctx context.Context, auditJobID uuid.UUID, at time.Time) error
	// InsertShareGrant records a creator's direction to share (§3.c evidence).
	InsertShareGrant(ctx context.Context, g ShareGrant) (uuid.UUID, error)
	// RevokeGrantsByUser withdraws every live grant a user ever made, and revokes
	// the reports built from their connected account. This is the hard stop the
	// Meta deauthorize / data-deletion callbacks and an explicit user request all
	// funnel into (§3.d "delete promptly on request").
	RevokeGrantsByUser(ctx context.Context, userID uuid.UUID, at time.Time) (int64, error)
}

// Service assembles and renders audit reports over the module's ports.
type Service struct {
	audit          port.AuditReader
	score          port.ScoreReader
	narrative      port.NarrativeReader
	fraud          port.FraudReader
	commentQuality port.CommentQualityReader
	pdf            port.PDFRenderer
	repo           Repository
	storage        port.Storage
	caller         port.CallerID
	owner          port.OwnerReader
	// mailer and recipients notify the creator that their report is ready. Both
	// are optional: a developer machine with no SMTP relay must still be able to
	// publish, so a nil mailer degrades the notification to a no-op rather than
	// failing the publish.
	mailer     port.Mailer
	recipients port.Recipient
	// handles freezes the creator's Instagram handle into the badge at publish
	// time so the /@handle alias resolves later. opens counts public badge reads
	// for the external-share-open metric. Both are optional: a nil handles reader
	// publishes an empty handle (slug-only badge), and a nil opens recorder makes
	// counting a no-op — neither may block a publish or a public read.
	handles port.HandleReader
	opens   port.OpenRecorder
}

// New wires the service. Every argument is a port the composition root
// satisfies with an adapter over the real module; repo and storage back the
// publish path (the durable, shareable report). caller and owner back the
// creator-ownership gate on publish/share (Meta Platform Terms §3.c).
func New(audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, fraud port.FraudReader, commentQuality port.CommentQualityReader, pdf port.PDFRenderer, repo Repository, storage port.Storage, caller port.CallerID, owner port.OwnerReader, mailer port.Mailer, recipients port.Recipient, handles port.HandleReader, opens port.OpenRecorder) *Service {
	return &Service{
		audit: audit, score: score, narrative: narrative, fraud: fraud,
		commentQuality: commentQuality, pdf: pdf, repo: repo, storage: storage,
		caller: caller, owner: owner, mailer: mailer, recipients: recipients,
		handles: handles, opens: opens,
	}
}

// Assemble builds the report for one of the caller's audits. The AuditReader
// scopes the read to the authenticated caller, so an audit the caller does not
// own surfaces as a not-found error before any score or narrative is read.
func (s *Service) Assemble(ctx context.Context, auditID string) (render.Report, error) {
	view, err := s.audit.AuditView(ctx, auditID)
	if err != nil {
		return render.Report{}, err
	}

	report := render.Report{
		AuditID:      view.ID.String(),
		InfluencerID: view.InfluencerID.String(),
		Status:       view.Status,
		Platforms:    view.Platforms,
	}
	if view.FinishedAt != nil {
		report.FinishedAt = view.FinishedAt.UTC().Format("2006-01-02 15:04 UTC")
	}

	// The score and narrative are advisory to the read: a failed audit has
	// neither, and the report discloses that rather than failing the request.
	sc, err := s.score.ScoreOf(ctx, view.InfluencerID)
	if err != nil {
		return render.Report{}, err
	}
	if sc.Present {
		report.Score = render.ScoreBlock{
			Available:        true,
			Overall:          sc.Overall,
			Authenticity:     sc.Authenticity,
			Niche:            sc.Niche,
			Tier:             sc.Tier,
			BenchmarkLabel:   sc.BenchmarkLabel,
			VerificationTier: sc.VerificationTier,
			Subscores:        make([]render.Subscore, 0, len(sc.Subscores)),
		}
		for _, ss := range sc.Subscores {
			report.Score.Subscores = append(report.Score.Subscores, render.Subscore{
				Name: ss.Name, Value: ss.Value, Confidence: ss.Confidence,
			})
		}
	}

	nar, err := s.narrative.NarrativeOf(ctx, view.ID)
	if err != nil {
		return render.Report{}, err
	}
	if nar.Present {
		report.NarrativeAvailable = true
		report.Narrative = render.Narrative{
			Summary:    nar.Summary,
			GrowthTips: nar.GrowthTips,
			BrandFit:   nar.BrandFit,
		}
		for _, wf := range nar.WeaknessFixPairs {
			report.Narrative.WeaknessFixPairs = append(report.Narrative.WeaknessFixPairs,
				render.WeaknessFix{Weakness: wf.Weakness, Fix: wf.Fix})
		}
	}

	// The coordination headline is shown only when a fraud pass actually produced
	// a signal. A job with no fraud row (Found=false) or one that ran but found
	// nothing (Present=false) omits the block rather than showing a zero the audit
	// cannot stand behind.
	fr, err := s.fraud.FraudOf(ctx, view.ID)
	if err != nil {
		return render.Report{}, err
	}
	if fr.Found && fr.Present {
		report.Fraud = render.FraudBlock{
			Available: true,
			RiskScore: fr.RiskScore,
			// Coordination was only assessed if the clique model actually ran, which
			// requires comments. Absent, the report says "not assessed" rather than
			// printing a 0 that reads as "no coordination found".
			CoordinationAnalyzed:     fr.CliqueCount != nil,
			CliqueCount:              fr.CliqueCount,
			CliqueMembershipFraction: fr.CliqueMembershipFraction,
			Confidence:               fr.Confidence,
			ModelVersion:             fr.ModelVersion,
		}
	}

	// Comment quality is a DISPLAY pill only (never scored). It is shown when a
	// classification ran and had comments to look at; the rate is stated only above
	// the classifier's minimum sample, otherwise the counts stand with a plain "rate
	// not established" — never a fabricated 0%. The reader is optional so the module
	// builds and tests without it.
	if s.commentQuality != nil {
		cq, err := s.commentQuality.CommentQualityOf(ctx, view.ID)
		if err != nil {
			return render.Report{}, err
		}
		if cq.Found && cq.Present {
			report.CommentQuality = render.CommentQualityBlock{
				Available:        true,
				AnalyzedCount:    cq.AnalyzedCount,
				LowQualityCount:  cq.LowQualityCount,
				LowQualityRatio:  cq.LowQualityRatio,
				SufficientSample: cq.SufficientSample,
				Counts:           cq.Counts,
				ModelVersion:     cq.ModelVersion,
			}
		}
	}

	return report, nil
}

// PDF assembles the report and renders it to a PDF document. It reuses Assemble,
// so the PDF and the JSON view can never disagree.
func (s *Service) PDF(ctx context.Context, auditID string) ([]byte, error) {
	report, err := s.Assemble(ctx, auditID)
	if err != nil {
		return nil, err
	}

	html, err := render.HTML(report)
	if err != nil {
		return nil, err
	}

	return s.pdf.RenderHTML(ctx, html)
}

// Publish renders the caller's report to PDF, stores it in object storage, and
// persists a durable public badge snapshot under a stable slug — the shareable
// deliverable. It reuses Assemble, so a published report can never disagree with
// the JSON/PDF read. A report with no score cannot be published: a badge with no
// number is not a credential, so it is rejected rather than shared empty.
func (s *Service) Publish(ctx context.Context, auditID string) (render.PublishResult, error) {
	report, err := s.Assemble(ctx, auditID)
	if err != nil {
		return render.PublishResult{}, err
	}
	if !report.Score.Available {
		return render.PublishResult{}, errs.New(errs.KindInvalid, "report.not_publishable",
			"an audit with no score cannot be published as a badge")
	}

	auditJobID, err := uuid.Parse(report.AuditID)
	if err != nil {
		return render.PublishResult{}, errs.New(errs.KindInvalid, "report.invalid_audit", "audit id is not a valid uuid")
	}

	// Only the creator who owns the connected account may publish a report built
	// from it. Assemble's caller-scoping proves the caller REQUESTED this audit;
	// it does not prove they own the audited account, and a brand may audit a
	// creator it does not own. Publishing is a disclosure of the creator's own
	// Instagram Graph data, which only the creator may direct (§3.c).
	if err := s.requireConnectedOwner(ctx, report.InfluencerID); err != nil {
		return render.PublishResult{}, err
	}

	html, err := render.HTML(report)
	if err != nil {
		return render.PublishResult{}, err
	}
	pdf, err := s.pdf.RenderHTML(ctx, html)
	if err != nil {
		return render.PublishResult{}, err
	}

	key := "reports/" + report.AuditID + ".pdf"
	if err := s.storage.Put(ctx, key, "application/pdf", pdf); err != nil {
		return render.PublishResult{}, err
	}

	slug, err := newSlug()
	if err != nil {
		return render.PublishResult{}, err
	}
	sum := sha256.Sum256(pdf)
	now := time.Now().UTC()

	// Freeze the creator's public Instagram handle into the badge so the /@handle
	// alias resolves later without re-reading any live account data. An influencer
	// with no handle on record publishes a slug-only badge (empty handle) rather
	// than a fabricated one; a handle lookup failure is not fatal to the publish.
	handle := s.instagramHandle(ctx, report.InfluencerID)

	durableSlug, err := s.repo.UpsertReport(ctx, ReportRecord{
		AuditJobID:  auditJobID,
		StorageKey:  key,
		PublicSlug:  slug,
		Badge:       badgeFrom(report, handle, now),
		SizeBytes:   int64(len(pdf)),
		Checksum:    hex.EncodeToString(sum[:]),
		GeneratedAt: now,
		// A published certificate expires. Until now this was left zero and stored
		// NULL, which granted indefinite public access to Graph-derived data —
		// exactly what §3.d forbids. Re-publishing renews it.
		ExpiresAt: now.Add(badgeTTL),
	})
	if err != nil {
		return render.PublishResult{}, err
	}

	shareURL, err := s.storage.ShareURL(key, shareTTL)
	if err != nil {
		return render.PublishResult{}, err
	}

	// The report is stored, the badge row is durable, and the link is minted: the
	// publish has SUCCEEDED. Telling the creator about it is a separate, lesser
	// concern, and a mail relay having a bad minute must not turn a completed
	// publish into a reported failure — that would invite a retry of an operation
	// that already took effect.
	s.notifyPublished(ctx, durableSlug, shareURL)

	return render.PublishResult{
		PublicSlug: durableSlug,
		BadgeURL:   "/reports/" + durableSlug,
		PDFURL:     shareURL,
		ExpiresAt:  now.Add(shareTTL).Format(time.RFC3339),
	}, nil
}

// notifyPublished tells the creator their report is ready. Every failure path
// here is logged and swallowed: see the call site in Publish.
//
// The recipient is the caller, and Publish has already proven through
// requireConnectedOwner that the caller IS the connected owner of the audited
// account — so this cannot mail a report to anyone but the creator whose data it
// was built from.
func (s *Service) notifyPublished(ctx context.Context, slug, shareURL string) {
	if s.mailer == nil || s.recipients == nil {
		return
	}

	callerID, err := s.caller.CallerID(ctx)
	if err != nil {
		slog.WarnContext(ctx, "could not resolve the caller to notify of a published report",
			slog.String("slug", slug), slog.Any("error", err))
		return
	}

	to, err := s.recipients.EmailOf(ctx, callerID)
	if err != nil || to == "" {
		slog.WarnContext(ctx, "could not resolve an email address to notify of a published report",
			slog.String("slug", slug), slog.Any("error", err))
		return
	}

	if err := s.mailer.Send(ctx, port.Message{
		To:      []string{to},
		Subject: "Your InfluAudit report is ready",
		Text: "Your audit report has been published.\n\n" +
			"Download the PDF: " + shareURL + "\n\n" +
			"This link expires in " + shareTTL.String() + ".\n",
	}); err != nil {
		// Not an error for the caller: the publish succeeded. It is an operational
		// signal that the relay is unhealthy.
		slog.WarnContext(ctx, "a report was published but the notification could not be delivered",
			slog.String("slug", slug), slog.Any("error", err))
	}
}

// PublicBadge serves the unauthenticated badge projection for a published slug.
// It reads a single frozen snapshot — no private data, no other module — and
// attaches a fresh presigned link to the stored PDF. An unknown slug is a
// not-found, never an empty badge.
func (s *Service) PublicBadge(ctx context.Context, slug string) (render.PublicBadge, error) {
	rec, found, err := s.repo.GetByPublicSlug(ctx, slug)
	if err != nil {
		return render.PublicBadge{}, err
	}
	if !found {
		return render.PublicBadge{}, errs.New(errs.KindNotFound, "report.badge_not_found", "no published report with that link")
	}
	// The badge resolved, so this read is a genuine external open. Count it against
	// the durable slug (stable across the slug and the /@handle alias) as non-owner:
	// the public read carries no proven caller identity.
	s.recordOpen(ctx, slug)
	return s.projectBadge(rec)
}

// PublicBadgeByHandle serves the same unauthenticated badge projection as
// PublicBadge, resolved by the creator's public Instagram handle (the /@handle
// alias) rather than by opaque slug. It returns the newest LIVE report for the
// handle; an unknown or fully-revoked/expired handle is a not-found, never an
// empty badge. Like the slug read, a successful resolve is counted as a non-owner
// external open — the metric the acquisition funnel exists to move.
func (s *Service) PublicBadgeByHandle(ctx context.Context, handle string) (render.PublicBadge, error) {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return render.PublicBadge{}, errs.New(errs.KindInvalid, "report.handle_required", "a handle is required")
	}
	rec, found, err := s.repo.GetByHandle(ctx, handle)
	if err != nil {
		return render.PublicBadge{}, err
	}
	if !found {
		return render.PublicBadge{}, errs.New(errs.KindNotFound, "report.badge_not_found", "no published report for that handle")
	}
	s.recordOpen(ctx, handle)
	return s.projectBadge(rec)
}

// projectBadge turns a stored snapshot into the public projection and attaches a
// fresh presigned link to the rendered PDF. No private data and no other module
// are read; the projection is exactly the frozen snapshot plus that link.
func (s *Service) projectBadge(rec PublishedReport) (render.PublicBadge, error) {
	pdfURL, err := s.storage.ShareURL(rec.StorageKey, shareTTL)
	if err != nil {
		return render.PublicBadge{}, err
	}
	return render.PublicBadge{
		Handle:           rec.Badge.Handle,
		Overall:          rec.Badge.Overall,
		Authenticity:     rec.Badge.Authenticity,
		Niche:            rec.Badge.Niche,
		Tier:             rec.Badge.Tier,
		BenchmarkLabel:   rec.Badge.BenchmarkLabel,
		VerificationTier: rec.Badge.VerificationTier,
		GeneratedAt:      rec.Badge.GeneratedAt,
		PDFURL:           pdfURL,
	}, nil
}

// recordOpen counts one public badge read as an external (non-owner) open. It is
// best-effort by design: the public read is unauthenticated, so an open is
// honestly attributed as non-owner, and a counting failure must never fail a read
// that already served the badge. A nil recorder (analytics not wired) is a no-op.
// ref is the durable slug or handle the badge was reached by.
func (s *Service) recordOpen(ctx context.Context, ref string) {
	if s.opens == nil {
		return
	}
	if err := s.opens.RecordOpen(ctx, ref, false); err != nil {
		slog.WarnContext(ctx, "a public badge was served but its open could not be recorded",
			slog.String("ref", ref), slog.Any("error", err))
	}
}

// requireConnectedOwner asserts the caller is the creator who owns the connected
// (OAuth-authenticated) account the influencer's data came from.
//
// This is the gate that keeps the product on the lawful side of Meta's Platform
// Terms. A report assembled from Instagram Graph Insights is the creator's
// Platform Data; §3.c permits disclosing it to a third party only "when a User
// expressly directs" it — so the person doing the directing must BE that user.
// Merely having requested the audit is not enough.
//
// An unclaimed profile (nobody has connected it) has no owner who could direct a
// disclosure, so it cannot be published or shared at all.
func (s *Service) requireConnectedOwner(ctx context.Context, influencerID string) error {
	id, err := uuid.Parse(influencerID)
	if err != nil {
		return errs.New(errs.KindInvalid, "report.invalid_influencer", "influencer id is not a valid uuid")
	}
	callerID, err := s.caller.CallerID(ctx)
	if err != nil {
		return err
	}
	owner, err := s.owner.ConnectedOwnerOf(ctx, id)
	if err != nil {
		return err
	}
	if owner.OwnerUserID == nil {
		return errs.New(errs.KindForbidden, "report.no_connected_owner",
			"this profile has no connected account owner, so its report cannot be published or shared")
	}
	if *owner.OwnerUserID != callerID {
		return errs.New(errs.KindForbidden, "report.not_account_owner",
			"only the creator who connected this account may publish or share its report")
	}
	return nil
}

// Revoke withdraws a published report: the public slug stops resolving and every
// live share grant on it is withdrawn. The creator who owns the connected account
// may call it at any time — Meta Platform Terms §3.d requires us to delete
// Platform Data promptly on the User's request, and a badge nobody can withdraw
// cannot satisfy that.
func (s *Service) Revoke(ctx context.Context, auditID string) error {
	view, err := s.audit.AuditView(ctx, auditID)
	if err != nil {
		return err
	}
	if err := s.requireConnectedOwner(ctx, view.InfluencerID.String()); err != nil {
		return err
	}
	return s.repo.RevokeByAuditJob(ctx, view.ID, time.Now().UTC())
}

// Share records the creator's express direction to disclose a published report to
// a NAMED brand for a STATED purpose, and returns the grant id as the receipt.
//
// This — not a sale, and not a brand-side lookup — is the only channel by which a
// creator's Graph-derived report reaches a third party. Meta Platform Terms
// §3.a.iv flatly prohibits selling or licensing Platform Data (including data
// "derived from" it, so a score computed from Insights is no escape hatch), while
// §3.c permits sharing "for the purposes as specified in the User's direction".
// So we take the direction, scope it, time-bound it, and keep the receipt.
func (s *Service) Share(ctx context.Context, auditID, recipient, purpose string) (render.ShareResult, error) {
	recipient = strings.TrimSpace(recipient)
	purpose = strings.TrimSpace(purpose)
	if recipient == "" {
		return render.ShareResult{}, errs.New(errs.KindInvalid, "report.recipient_required",
			"name the brand or agency this report may be shared with")
	}
	if purpose == "" {
		return render.ShareResult{}, errs.New(errs.KindInvalid, "report.purpose_required",
			"state the purpose this report may be used for")
	}

	view, err := s.audit.AuditView(ctx, auditID)
	if err != nil {
		return render.ShareResult{}, err
	}
	if err := s.requireConnectedOwner(ctx, view.InfluencerID.String()); err != nil {
		return render.ShareResult{}, err
	}

	reportID, found, err := s.repo.ReportIDOf(ctx, view.ID)
	if err != nil {
		return render.ShareResult{}, err
	}
	if !found {
		return render.ShareResult{}, errs.New(errs.KindInvalid, "report.not_published",
			"publish the report before sharing it")
	}

	callerID, err := s.caller.CallerID(ctx)
	if err != nil {
		return render.ShareResult{}, err
	}

	now := time.Now().UTC()
	expires := now.Add(grantTTL)
	grantID, err := s.repo.InsertShareGrant(ctx, ShareGrant{
		ReportID:        reportID,
		GrantedByUserID: callerID,
		Recipient:       recipient,
		Purpose:         purpose,
		GrantedAt:       now,
		ExpiresAt:       expires,
	})
	if err != nil {
		return render.ShareResult{}, err
	}

	return render.ShareResult{
		GrantID:   grantID.String(),
		Recipient: recipient,
		Purpose:   purpose,
		ExpiresAt: expires.Format(time.RFC3339),
	}, nil
}

// RevokeAllForUser withdraws every share grant a user made and every report built
// from their connected account. It is the single hard stop behind an explicit
// user request and behind Meta's deauthorize / data-deletion callbacks (§3.d):
// once a creator disconnects or asks for deletion, nothing they shared stays
// reachable. It takes no caller — the callbacks are unauthenticated by design and
// prove the user's identity by Meta's signature instead.
func (s *Service) RevokeAllForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.repo.RevokeGrantsByUser(ctx, userID, time.Now().UTC())
}

// instagramHandle resolves the influencer's public Instagram handle for the badge
// snapshot. It is best-effort: a nil handles reader (analytics/influencer not
// wired), an unparseable id, a lookup error, or an influencer with no handle on
// record all yield an empty handle, and the badge is then reachable only by its
// opaque slug. It never fails the publish and never invents a handle.
func (s *Service) instagramHandle(ctx context.Context, influencerID string) string {
	if s.handles == nil {
		return ""
	}
	id, err := uuid.Parse(influencerID)
	if err != nil {
		return ""
	}
	handle, found, err := s.handles.InstagramHandleOf(ctx, id)
	if err != nil {
		slog.WarnContext(ctx, "could not resolve an Instagram handle for a published badge",
			slog.String("influencer_id", influencerID), slog.Any("error", err))
		return ""
	}
	if !found {
		return ""
	}
	return strings.TrimSpace(handle)
}

// badgeFrom snapshots the public-safe subset of an assembled report: the
// headline score and its benchmark context, plus the creator's public Instagram
// handle resolved at publish time. Only fields already present on the assembled
// report (or the freshly-resolved handle) are taken, never derived or invented; an
// unknown handle is frozen as the empty string, not a placeholder.
func badgeFrom(r render.Report, handle string, now time.Time) BadgeSnapshot {
	generated := r.FinishedAt
	if generated == "" {
		generated = now.Format("2006-01-02 15:04 UTC")
	}
	return BadgeSnapshot{
		Handle:           handle,
		Overall:          r.Score.Overall,
		Authenticity:     r.Score.Authenticity,
		Niche:            r.Score.Niche,
		Tier:             r.Score.Tier,
		BenchmarkLabel:   r.Score.BenchmarkLabel,
		VerificationTier: r.Score.VerificationTier,
		GeneratedAt:      generated,
	}
}

// newSlug mints a URL-safe, unguessable public slug. A published badge is
// reachable by anyone holding the link, so the slug must not be enumerable: it is
// 16 random bytes, base64url without padding.
func newSlug() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errs.Wrap(err, errs.KindInternal, "report.slug", "could not generate a public slug")
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
