package render

import (
	"strings"
	"testing"
)

func fullReport() Report {
	return Report{
		AuditID:      "aud-1",
		InfluencerID: "inf-1",
		Status:       "succeeded",
		Platforms:    []string{"youtube"},
		Score: ScoreBlock{
			Available:      true,
			Overall:        82.4,
			Authenticity:   74.1,
			Niche:          "beauty",
			Tier:           "micro",
			BenchmarkLabel: "industry-bootstrap v1",
			Subscores: []Subscore{
				{Name: "reach", Value: 80, Confidence: 0.6},
				{Name: "authenticity", Value: 74.1, Confidence: 0.5},
			},
		},
		Fraud: FraudBlock{
			Available:                true,
			CliqueCount:              7,
			CliqueMembershipFraction: 0.42,
			FakeFollowerRate:         0.11,
			BotCommentRate:           0.42,
			EngagementAnomaly:        0.2,
			Confidence:               0.6,
			ModelVersion:             "clique-v1",
		},
		NarrativeAvailable: true,
		Narrative: Narrative{
			Summary:          "Your engagement is below the beauty micro benchmark.",
			WeaknessFixPairs: []WeaknessFix{{Weakness: "Low comments", Fix: "Ask questions in captions"}},
			GrowthTips:       []string{"Post Reels 3x/week"},
			BrandFit:         "Best fit for clean-beauty brands.",
		},
	}
}

func TestHTMLRendersScoreAndNarrative(t *testing.T) {
	out, err := HTML(fullReport())
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	html := string(out)

	for _, want := range []string{
		"82.4",                            // overall
		"74.1",                            // authenticity
		"beauty",                          // niche
		"industry-bootstrap v1",           // benchmark provenance disclosed
		"estimates, not measured",         // fraud-is-an-estimate labelling
		"Authenticity &amp; coordination", // fraud headline section
		"Coordinated commenter cliques",   // clique-count row
		"clique-v1",                       // fraud model provenance
		"Low comments",                    // weakness
		"Ask questions in captions",
		"Post Reels 3x/week",
		"clean-beauty",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered report missing %q", want)
		}
	}
}

func TestHTMLDisclosesAbsentScoreAndNarrative(t *testing.T) {
	out, err := HTML(Report{AuditID: "aud-2", Status: "failed"})
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	html := string(out)

	if !strings.Contains(html, "produced no score") {
		t.Error("a scoreless report must disclose the absence, not invent a number")
	}
	if !strings.Contains(html, "narrative is still pending") {
		t.Error("a report with no narrative must say so")
	}
	// It must not render a subscore table or a zero score hero.
	if strings.Contains(html, "Score breakdown") {
		t.Error("a scoreless report must not render the breakdown table")
	}
}

// The report is user-controlled data (handles, niche, llm output); it must be
// contextually escaped so a crafted value cannot inject markup into the PDF.
func TestHTMLEscapesContent(t *testing.T) {
	r := fullReport()
	r.Narrative.Summary = `<script>alert(1)</script>`
	out, err := HTML(r)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	if strings.Contains(string(out), "<script>alert(1)</script>") {
		t.Error("narrative content was not escaped; markup injection is possible")
	}
}

func TestFormatOne(t *testing.T) {
	cases := map[float64]string{
		0:     "0.0",
		82.44: "82.4",
		82.45: "82.5",
		74.1:  "74.1",
		100:   "100.0",
	}
	for in, want := range cases {
		if got := formatOne(in); got != want {
			t.Errorf("formatOne(%v) = %q, want %q", in, got, want)
		}
	}
}
