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

	"github.com/getnyx/influaudit/backend/internal/report/internal/render"
	"github.com/getnyx/influaudit/backend/internal/report/port"
)

// Service assembles and renders audit reports over the module's ports.
type Service struct {
	audit     port.AuditReader
	score     port.ScoreReader
	narrative port.NarrativeReader
	pdf       port.PDFRenderer
}

// New wires the service. Every argument is a port the composition root
// satisfies with an adapter over the real module.
func New(audit port.AuditReader, score port.ScoreReader, narrative port.NarrativeReader, pdf port.PDFRenderer) *Service {
	return &Service{audit: audit, score: score, narrative: narrative, pdf: pdf}
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
