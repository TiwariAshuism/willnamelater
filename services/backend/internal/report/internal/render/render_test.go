package render

import (
	"strings"
	"testing"
)

// f64p / intp are observed measurements. Every fraud figure is a pointer now:
// nil is "we could not look", and the deliverable must say so in words instead of
// printing a 0% that a brand reads as a clean bill of health.
func f64p(v float64) *float64 { return &v }
func intp(v int) *int         { return &v }

func fullReport() Report {
	return Report{
		AuditID:      "aud-1",
		InfluencerID: "inf-1",
		Status:       "succeeded",
		Platforms:    []string{"youtube"},
		Score: ScoreBlock{
			Available:      true,
			Overall:        82.4,
			Authenticity:   ptrF(74.1),
			Niche:          "beauty",
			Tier:           "micro",
			BenchmarkLabel: "industry-bootstrap v1",
			Subscores: []Subscore{
				{Name: "reach", Value: 80, Confidence: 0.6},
				{Name: "authenticity", Value: 74.1, Confidence: 0.5},
			},
		},
		// EXPECTATION CHANGED: FakeFollowerRate, BotCommentRate and EngagementAnomaly
		// are gone from the block — none of the three was ever measured. What remains
		// is the honest composite RiskScore plus the clique signals, and those only
		// when CoordinationAnalyzed says the commenter graph was actually built.
		Fraud: FraudBlock{
			Available:                true,
			RiskScore:                f64p(63.5),
			CoordinationAnalyzed:     true,
			CliqueCount:              intp(7),
			CliqueMembershipFraction: f64p(0.42),
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
		"Coordinated commenter cliques",   // clique-count row (coordination WAS analyzed)
		"Authenticity risk score",         // the honest composite, labelled a score not a rate
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
	// Coordination WAS analyzed here, so the not-assessed disclosure must be absent.
	if strings.Contains(html, "not assessed") {
		t.Error("an analyzed report must not carry the not-assessed disclosure")
	}
}

// THE guarantee. Almost every audit analyzes no commenters at all — Instagram and
// CSV audits pull no comment events — so the clique model never runs. The report
// used to print "0" cliques and "0.0%" membership for those audits, which a brand
// reads as "we checked and found no coordination". It must instead say, in words,
// that it could not look.
func TestHTMLDisclosesUnassessedCoordination(t *testing.T) {
	r := fullReport()
	r.Fraud = FraudBlock{
		Available:            true,
		RiskScore:            f64p(41),
		CoordinationAnalyzed: false, // no comment data reached the clique model
		Confidence:           0.25,
		ModelVersion:         "risk-v2",
	}

	out, err := HTML(r)
	if err != nil {
		t.Fatalf("HTML: %v", err)
	}
	html := string(out)

	// The disclosure, in the brand's language, not a silent omission.
	for _, want := range []string{"not assessed", "could not look"} {
		if !strings.Contains(html, want) {
			t.Errorf("unassessed coordination must be disclosed; missing %q", want)
		}
	}
	// And NOT a zero anywhere. A "0%" here is the exact lie the disclosure replaces.
	// The block's confidence is deliberately 0.25 ("25.0%") so that these checks
	// cannot be tripped by an unrelated figure: every percentage the template prints
	// carries one decimal, so a bare "0%" substring would also match "25.0%".
	if strings.Contains(html, "0.0%") {
		t.Error(`rendered "0.0%" for coordination that was never assessed: absence is not a measured zero`)
	}
	if strings.Contains(html, "<td>0</td>") {
		t.Error("rendered a 0 clique count for a commenter graph that was never built")
	}
	// The rows are omitted outright, not printed empty.
	for _, gone := range []string{"Coordinated commenter cliques", "Commenters inside a coordinated clique"} {
		if strings.Contains(html, gone) {
			t.Errorf("row %q must be omitted entirely when no commenter graph was built", gone)
		}
	}
}

// The two removed figures were never measurements: fake_follower_rate was the
// composite risk score renamed (no follower list is ever fetched) and
// bot_comment_rate was a verbatim duplicate of clique_membership_fraction (no
// comment's text is ever classified). Neither word may appear anywhere in a
// document a brand pays for.
func TestHTMLNeverClaimsFakeFollowersOrBotComments(t *testing.T) {
	for _, r := range []Report{fullReport(), {AuditID: "aud-3", Status: "failed"}} {
		out, err := HTML(r)
		if err != nil {
			t.Fatalf("HTML: %v", err)
		}
		html := strings.ToLower(string(out))
		for _, banned := range []string{"fake-follower", "fake follower", "fake_follower", "bot-comment", "bot comment", "bot_comment"} {
			if strings.Contains(html, banned) {
				t.Errorf("rendered report claims %q — nothing in this system ever measured it", banned)
			}
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

// ptrF supplies a real pointer: Authenticity is nil when the dimension rested on
// no measurement, so a test asserting a value must say so explicitly.
func ptrF(v float64) *float64 { return &v }
