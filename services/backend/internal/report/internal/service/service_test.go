package service

import (
	"context"
	"errors"
	"strings"
	"testing"

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

type fakePDF struct {
	gotHTML []byte
	out     []byte
	err     error
}

func (f *fakePDF) RenderHTML(_ context.Context, html []byte) ([]byte, error) {
	f.gotHTML = html
	return f.out, f.err
}

func infID() uuid.UUID { return uuid.MustParse("11111111-1111-1111-1111-111111111111") }
func audID() uuid.UUID { return uuid.MustParse("22222222-2222-2222-2222-222222222222") }

func fullView() port.AuditView {
	return port.AuditView{
		ID:           audID(),
		InfluencerID: infID(),
		Status:       "succeeded",
		Platforms:    []string{"youtube"},
	}
}

func TestAssembleFoldsAllSources(t *testing.T) {
	svc := New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82, Authenticity: 74, Niche: "beauty",
			Subscores: []port.Subscore{{Name: "reach", Value: 80, Confidence: 0.6}}}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "s", GrowthTips: []string{"t"},
			WeaknessFixPairs: []port.WeaknessFix{{Weakness: "w", Fix: "f"}}}},
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
	if rep.InfluencerID != infID().String() || rep.Status != "succeeded" {
		t.Errorf("audit fields not mapped: %+v", rep)
	}
}

// A failed audit has no score and no narrative; the report must still assemble,
// disclosing both as absent rather than erroring.
func TestAssembleToleratesAbsentScoreAndNarrative(t *testing.T) {
	svc := New(
		fakeAudit{view: port.AuditView{ID: audID(), InfluencerID: infID(), Status: "failed"}},
		fakeScore{view: port.ScoreView{Present: false}},
		fakeNarrative{view: port.Narrative{Present: false}},
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
}

// An unauthorized/unknown audit surfaces from the AuditReader; the report must
// not read a score or narrative for it.
func TestAssemblePropagatesAuditError(t *testing.T) {
	svc := New(
		fakeAudit{err: errs.New(errs.KindNotFound, "audit.not_found", "no such audit")},
		fakeScore{},
		fakeNarrative{},
		&fakePDF{},
	)
	_, err := svc.Assemble(context.Background(), "whatever")
	if err == nil || errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("want not-found from audit reader, got %v", err)
	}
}

func TestPDFRendersAssembledReport(t *testing.T) {
	pdf := &fakePDF{out: []byte("%PDF-1.4 fake")}
	svc := New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true, Overall: 82}},
		fakeNarrative{view: port.Narrative{Present: true, Summary: "hello-summary"}},
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
	svc := New(
		fakeAudit{view: fullView()},
		fakeScore{view: port.ScoreView{Present: true}},
		fakeNarrative{view: port.Narrative{Present: false}},
		&fakePDF{err: errors.New("gotenberg down")},
	)
	if _, err := svc.PDF(context.Background(), audID().String()); err == nil {
		t.Fatal("want error when the PDF renderer fails")
	}
}
