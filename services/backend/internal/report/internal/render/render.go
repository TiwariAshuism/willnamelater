// Package render assembles the audit deliverable's HTML. It is a leaf: it
// depends only on the standard library, so both the JSON read path and the PDF
// path share one canonical Report shape and one template. The template is
// self-contained (inline CSS, no external assets) because Gotenberg renders it
// in isolation.
package render

import (
	"bytes"
	"html/template"
	"strconv"

	"github.com/getnyx/influaudit/backend/internal/platform/errs"
)

// Report is the assembled audit deliverable: the shape returned by the JSON read
// route and rendered into the PDF. Its json tags define the read route's
// response contract.
type Report struct {
	AuditID      string     `json:"audit_id"`
	InfluencerID string     `json:"influencer_id"`
	Status       string     `json:"status"`
	Platforms    []string   `json:"platforms"`
	Score        ScoreBlock `json:"score"`
	// Fraud is the coordinated-inauthenticity headline. FraudBlock.Available is
	// false when no fraud pass produced a signal, in which case the block is
	// omitted rather than shown as a misleading zero.
	Fraud FraudBlock `json:"fraud"`
	// CommentQuality is a DISPLAY-only pill (never scored). Available is false when
	// no classification ran or no comments were available; the block is then omitted.
	CommentQuality CommentQualityBlock `json:"comment_quality"`
	// Narrative is the advisory content. NarrativeAvailable is false when the
	// ml/llm step was skipped or failed; the report then shows the score alone.
	Narrative          Narrative `json:"narrative"`
	NarrativeAvailable bool      `json:"narrative_available"`
	FinishedAt         string    `json:"finished_at,omitempty"`
}

// CommentQualityBlock is the display-only comment-quality summary. It NEVER feeds
// the score (the ML firewall). LowQualityRatio is a pointer: nil below the
// classifier's minimum sample, where the report shows the bucket counts and states
// the rate is not established rather than printing a fabricated 0%. It always
// carries the denominator (AnalyzedCount), never extrapolates the sampled comments
// to the whole account, and is captioned that a high rate is not fraud.
type CommentQualityBlock struct {
	Available        bool           `json:"available"`
	AnalyzedCount    int            `json:"analyzed_count"`
	LowQualityCount  int            `json:"low_quality_count"`
	LowQualityRatio  *float64       `json:"low_quality_ratio,omitempty"`
	SufficientSample bool           `json:"sufficient_sample"`
	Counts           map[string]int `json:"counts,omitempty"`
	ModelVersion     string         `json:"model_version,omitempty"`
}

// FraudBlock is the coordinated-inauthenticity estimate presented as a headline.
// Available is false when no fraud pass produced a signal; the report then omits
// the block rather than showing a misleading zero. Every figure here is a model
// estimate, labelled as such in the rendered document. CliqueCount (maximal
// commenter cliques of the model's minimum size) is the primary signal; the
// rates are fractions in [0,1] rendered as percentages.
// Every measurement is a pointer: nil means we could not measure it, and the
// deliverable says so in words rather than printing a 0% that reads as a clean
// bill of health.
//
// Three fields were removed outright because they were never measurements:
//   - fake_follower_rate was the composite risk score renamed (no follower list is
//     ever fetched, so no fake-follower rate has ever been computed);
//   - bot_comment_rate was a verbatim duplicate of clique_membership_fraction (no
//     comment's text is ever classified), and printing both manufactured fake
//     corroboration between two identical numbers; and
//   - engagement_anomaly was a structural constant 0%, printed to brands as though
//     we had checked engagement against a benchmark we never supplied.
type FraudBlock struct {
	Available bool `json:"available"`

	// RiskScore is the ml service's composite per-account risk estimate, 0-100,
	// higher = more likely inauthentic. It is an estimate over behavioural signals,
	// NOT a measured rate of anything.
	RiskScore *float64 `json:"risk_score,omitempty"`

	// CoordinationAnalyzed is false when no commenters could be analyzed at all —
	// the usual case for Instagram and CSV audits, which pull no comment events. The
	// clique figures are then nil and the report states plainly that coordination
	// was not assessed, instead of implying none was found.
	CoordinationAnalyzed     bool     `json:"coordination_analyzed"`
	CliqueCount              *int     `json:"clique_count,omitempty"`
	CliqueMembershipFraction *float64 `json:"clique_membership_fraction,omitempty"`

	Confidence   float64 `json:"confidence"`
	ModelVersion string  `json:"model_version"`
}

// ScoreBlock is the composite score presented in the report. Available is false
// for a fully failed audit that produced no score.
type ScoreBlock struct {
	Available bool    `json:"available"`
	Overall   float64 `json:"overall"`
	// Authenticity is nil when the authenticity dimension rests on NO measurement.
	// The engine's neutral 50 means "we don't know"; printing it would certify an
	// account nobody examined, so the deliverable says "not assessed" instead.
	Authenticity   *float64 `json:"authenticity,omitempty"`
	Niche          string   `json:"niche,omitempty"`
	Tier           string   `json:"tier,omitempty"`
	BenchmarkLabel string   `json:"benchmark_label,omitempty"`
	// VerificationTier is the trust tier ("verified"/"estimated"/"unverified")
	// derived from the provenance of the data behind the score.
	VerificationTier string     `json:"verification_tier,omitempty"`
	Subscores        []Subscore `json:"subscores"`
}

// Subscore is one dimension of the composite, with the confidence that
// qualifies it.
type Subscore struct {
	Name       string  `json:"name"`
	Value      float64 `json:"value"`
	Confidence float64 `json:"confidence"`
}

// Narrative is the llm-generated advisory content.
type Narrative struct {
	Summary          string        `json:"summary"`
	WeaknessFixPairs []WeaknessFix `json:"weakness_fix_pairs"`
	GrowthTips       []string      `json:"growth_tips"`
	BrandFit         string        `json:"brand_fit"`
}

// WeaknessFix pairs a weakness with its concrete fix.
type WeaknessFix struct {
	Weakness string `json:"weakness"`
	Fix      string `json:"fix"`
}

// HTML renders the report into a self-contained HTML document for the PDF
// pipeline. It uses html/template, so every field is contextually escaped and a
// report can never inject markup into its own deliverable.
func HTML(r Report) ([]byte, error) {
	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, r); err != nil {
		return nil, errs.Wrap(err, errs.KindInternal, "report.render_failed",
			"could not render the report document")
	}
	return buf.Bytes(), nil
}

// reportTemplate is parsed once at package load. A parse failure is a programmer
// error in the literal below, so it panics rather than deferring the failure to
// the first render.
var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"pct": func(f float64) string {
		return template.HTMLEscapeString(formatOne(f))
	},
	// frac formats a fraction in [0,1] as a percentage with one decimal, for the
	// fraud estimates (which are fractions, unlike the 0-100 subscores).
	"frac": func(f float64) string {
		return template.HTMLEscapeString(formatOne(f * 100))
	},
	// scoreP and fracP take POINTERS, because an unmeasured signal is nil. They
	// render it as an em dash — never as a 0, which is the whole point of the
	// pointers — and never panic on a nil deref the way `frac` on a nil *float64
	// would. `printf "%.1f"` must NOT be used on a pointer: printf takes `any`, so
	// the template does not auto-dereference and it would emit
	// `%!f(*float64=0xc000188648)` into the customer's PDF.
	"scoreP": func(f *float64) string {
		if f == nil {
			return notMeasured
		}
		return template.HTMLEscapeString(formatOne(*f))
	},
	"fracP": func(f *float64) string {
		if f == nil {
			return notMeasured
		}
		return template.HTMLEscapeString(formatOne(*f * 100))
	},
	"countP": func(i *int) string {
		if i == nil {
			return notMeasured
		}
		return template.HTMLEscapeString(strconv.Itoa(*i))
	},
}).Parse(reportHTML))

// notMeasured is what an absent measurement renders as. It is deliberately not a
// number: a "0" here would read as a clean measurement we never took.
const notMeasured = "not measured"

// formatOne formats a float to one decimal place without importing fmt into the
// hot path repeatedly; it is small and dependency-light on purpose.
func formatOne(f float64) string {
	// Round to one decimal.
	scaled := int64(f*10 + 0.5)
	if f < 0 {
		scaled = int64(f*10 - 0.5)
	}
	whole := scaled / 10
	frac := scaled % 10
	if frac < 0 {
		frac = -frac
	}
	return itoa(whole) + "." + itoa(frac)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

const reportHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>InfluAudit Report</title>
<style>
  * { box-sizing: border-box; }
  body { font-family: -apple-system, Segoe UI, Roboto, Helvetica, Arial, sans-serif; color: #1a1a2e; margin: 0; padding: 40px; }
  h1 { font-size: 24px; margin: 0 0 4px; }
  h2 { font-size: 16px; margin: 28px 0 10px; border-bottom: 2px solid #e6e6ef; padding-bottom: 6px; }
  .sub { color: #6b6b80; font-size: 12px; margin: 0 0 20px; }
  .score-hero { display: flex; gap: 32px; margin: 16px 0 8px; }
  .score-hero .big { font-size: 48px; font-weight: 700; line-height: 1; }
  .score-hero .lbl { font-size: 11px; color: #6b6b80; text-transform: uppercase; letter-spacing: .04em; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { text-align: left; padding: 7px 8px; border-bottom: 1px solid #eee; }
  th { color: #6b6b80; font-weight: 600; font-size: 11px; text-transform: uppercase; letter-spacing: .03em; }
  .conf { color: #6b6b80; }
  ul { margin: 6px 0; padding-left: 18px; }
  li { margin: 4px 0; font-size: 13px; }
  .wf { margin: 8px 0; padding: 10px 12px; background: #f7f7fb; border-radius: 6px; }
  .wf .w { font-weight: 600; font-size: 13px; }
  .wf .f { font-size: 13px; color: #333; margin-top: 2px; }
  .note { font-size: 11px; color: #6b6b80; margin-top: 4px; font-style: italic; }
  .banner { background: #fff6e6; border: 1px solid #f0d9a8; color: #7a5a10; padding: 8px 12px; border-radius: 6px; font-size: 12px; margin: 10px 0; }
</style>
</head>
<body>
  <h1>InfluAudit Report</h1>
  <p class="sub">Audit {{.AuditID}} &middot; Status: {{.Status}}{{if .FinishedAt}} &middot; {{.FinishedAt}}{{end}}</p>

  {{if .Score.Available}}
  <div class="score-hero">
    <div><div class="lbl">Influence Score</div><div class="big">{{pct .Score.Overall}}</div></div>
    <div><div class="lbl">Authenticity</div><div class="big">{{scoreP .Score.Authenticity}}</div></div>
  </div>
  {{if .Score.Niche}}<p class="sub">{{.Score.Niche}}{{if .Score.Tier}} &middot; {{.Score.Tier}} tier{{end}}</p>{{end}}
  {{if .Score.BenchmarkLabel}}<p class="note">Benchmarks: {{.Score.BenchmarkLabel}}. Fraud figures are estimates, not measured percentages.</p>{{end}}

  <h2>Score breakdown</h2>
  <table>
    <thead><tr><th>Dimension</th><th>Value</th><th>Confidence</th></tr></thead>
    <tbody>
    {{range .Score.Subscores}}
      <tr><td>{{.Name}}</td><td>{{pct .Value}}</td><td class="conf">{{pct .Confidence}}</td></tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="banner">This audit produced no score. No platform returned usable data, so no number is shown rather than an invented one.</div>
  {{end}}

  {{if .Fraud.Available}}
  <h2>Authenticity &amp; coordination</h2>
  <p class="note">Model estimates, not measured percentages. Model {{.Fraud.ModelVersion}} &middot; confidence {{frac .Fraud.Confidence}}%.</p>
  <table>
    <thead><tr><th>Signal</th><th>Estimate</th></tr></thead>
    <tbody>
      {{if .Fraud.RiskScore}}
      <tr><td>Authenticity risk score (0-100, higher = more suspicious)</td><td>{{scoreP .Fraud.RiskScore}}</td></tr>
      {{end}}
      {{if .Fraud.CoordinationAnalyzed}}
      <tr><td>Coordinated commenter cliques</td><td>{{countP .Fraud.CliqueCount}}</td></tr>
      <tr><td>Commenters inside a coordinated clique</td><td>{{fracP .Fraud.CliqueMembershipFraction}}%</td></tr>
      {{end}}
    </tbody>
  </table>
  {{if not .Fraud.CoordinationAnalyzed}}
  <p class="note">Coordination was <strong>not assessed</strong>: this platform returned no comment data, so no commenter graph could be built. This is not a finding of "no coordination" — it means we could not look.</p>
  {{end}}
  {{end}}

  {{if .CommentQuality.Available}}
  <h2>Comment quality</h2>
  <p class="note">A rule-based read of {{.CommentQuality.AnalyzedCount}} sampled comments &mdash; these comments only, never extrapolated to the account. Model {{.CommentQuality.ModelVersion}}. The rule set leans English, so it under-reads other languages, and a high generic rate is <strong>not</strong> fraud: fan and meme audiences leave oceans of genuine emoji.</p>
  {{if and .CommentQuality.SufficientSample .CommentQuality.LowQualityRatio}}
  <p>Low-quality (generic / emoji-only / duplicate) comments: {{fracP .CommentQuality.LowQualityRatio}}% of the {{.CommentQuality.AnalyzedCount}} sampled.</p>
  {{else}}
  <p>Of {{.CommentQuality.AnalyzedCount}} sampled, {{.CommentQuality.LowQualityCount}} read as low-quality. Too few to state a reliable rate, so no percentage is shown rather than an unreliable one.</p>
  {{end}}
  {{end}}

  {{if .NarrativeAvailable}}
  <h2>Summary</h2>
  <p>{{.Narrative.Summary}}</p>

  {{if .Narrative.WeaknessFixPairs}}
  <h2>What to fix</h2>
  {{range .Narrative.WeaknessFixPairs}}
    <div class="wf"><div class="w">{{.Weakness}}</div><div class="f">&rarr; {{.Fix}}</div></div>
  {{end}}
  {{end}}

  {{if .Narrative.GrowthTips}}
  <h2>Growth tips</h2>
  <ul>{{range .Narrative.GrowthTips}}<li>{{.}}</li>{{end}}</ul>
  {{end}}

  {{if .Narrative.BrandFit}}
  <h2>Brand fit</h2>
  <p>{{.Narrative.BrandFit}}</p>
  {{end}}
  {{else}}
  <div class="banner">The advisory narrative is still pending or was unavailable for this audit. The score above stands on its own.</div>
  {{end}}
</body>
</html>`
