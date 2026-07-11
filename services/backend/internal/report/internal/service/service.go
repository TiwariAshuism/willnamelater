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

// BadgeSnapshot is the public-safe projection frozen at publish time and stored
// as badge_jsonb. It carries only non-sensitive headline fields — never the
// advisory narrative or anything identifying the account owner beyond the public
// handle the creator already publishes.
type BadgeSnapshot struct {
	Handle         string  `json:"handle"`
	Overall        float64 `json:"overall"`
	Authenticity   float64 `json:"authenticity"`
	Niche          string  `json:"niche"`
	Tier           string  `json:"tier"`
	BenchmarkLabel string  `json:"benchmark_label"`
	GeneratedAt    string  `json:"generated_at"`
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

// Repository persists and reads published reports. It is declared by the service
// (its consumer) and satisfied by the repository package. UpsertReport returns
// the durable public slug — the pre-existing one when a report is re-published,
// so a shared link is stable — which may differ from the candidate on rec.
type Repository interface {
	UpsertReport(ctx context.Context, rec ReportRecord) (publicSlug string, err error)
	GetByPublicSlug(ctx context.Context, slug string) (PublishedReport, bool, error)
}

// Service assembles and renders audit reports over the module's ports.
type Service struct {
	audit     port.AuditReader
	score     port.ScoreReader
	narrative port.NarrativeReader
	fraud     port.FraudReader
	pdf       port.PDFRenderer
	repo      Repository
	storage   port.Storage
}

// New wires the service. Every argument is a port the composition root
// satisfies with an adapter over the real module; repo and storage back the
// publish path (the durable, shareable report).
func New(audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, fraud port.FraudReader, pdf port.PDFRenderer, repo Repository, storage port.Storage) *Service {
	return &Service{audit: audit, score: score, narrative: narrative, fraud: fraud, pdf: pdf, repo: repo, storage: storage}
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
			Available:      true,
			Overall:        sc.Overall,
			Authenticity:   sc.Authenticity,
			Niche:          sc.Niche,
			Tier:           sc.Tier,
			BenchmarkLabel: sc.BenchmarkLabel,
			Subscores:      make([]render.Subscore, 0, len(sc.Subscores)),
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
			Available:                true,
			CliqueCount:              fr.CliqueCount,
			CliqueMembershipFraction: fr.CliqueMembershipFraction,
			FakeFollowerRate:         fr.FakeFollowerRate,
			BotCommentRate:           fr.BotCommentRate,
			EngagementAnomaly:        fr.EngagementAnomaly,
			Confidence:               fr.Confidence,
			ModelVersion:             fr.ModelVersion,
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

	durableSlug, err := s.repo.UpsertReport(ctx, ReportRecord{
		AuditJobID:  auditJobID,
		StorageKey:  key,
		PublicSlug:  slug,
		Badge:       badgeFrom(report, now),
		SizeBytes:   int64(len(pdf)),
		Checksum:    hex.EncodeToString(sum[:]),
		GeneratedAt: now,
	})
	if err != nil {
		return render.PublishResult{}, err
	}

	shareURL, err := s.storage.ShareURL(key, shareTTL)
	if err != nil {
		return render.PublishResult{}, err
	}

	return render.PublishResult{
		PublicSlug: durableSlug,
		BadgeURL:   "/reports/" + durableSlug,
		PDFURL:     shareURL,
		ExpiresAt:  now.Add(shareTTL).Format(time.RFC3339),
	}, nil
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

	pdfURL, err := s.storage.ShareURL(rec.StorageKey, shareTTL)
	if err != nil {
		return render.PublicBadge{}, err
	}

	return render.PublicBadge{
		Handle:         rec.Badge.Handle,
		Overall:        rec.Badge.Overall,
		Authenticity:   rec.Badge.Authenticity,
		Niche:          rec.Badge.Niche,
		Tier:           rec.Badge.Tier,
		BenchmarkLabel: rec.Badge.BenchmarkLabel,
		GeneratedAt:    rec.Badge.GeneratedAt,
		PDFURL:         pdfURL,
	}, nil
}

// badgeFrom snapshots the public-safe subset of an assembled report: the
// headline score and its benchmark context. Only fields already present on the
// assembled report are taken, never derived or invented; the handle is left
// empty because the report document does not carry one.
func badgeFrom(r render.Report, now time.Time) BadgeSnapshot {
	generated := r.FinishedAt
	if generated == "" {
		generated = now.Format("2006-01-02 15:04 UTC")
	}
	return BadgeSnapshot{
		Overall:        r.Score.Overall,
		Authenticity:   r.Score.Authenticity,
		Niche:          r.Score.Niche,
		Tier:           r.Score.Tier,
		BenchmarkLabel: r.Score.BenchmarkLabel,
		GeneratedAt:    generated,
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
