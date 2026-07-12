package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/getnyx/influaudit/backend/internal/audit"
	"github.com/getnyx/influaudit/backend/internal/audit/port"
	"github.com/getnyx/influaudit/backend/internal/auth"
	"github.com/getnyx/influaudit/backend/internal/billing"
	"github.com/getnyx/influaudit/backend/internal/connector"
	"github.com/getnyx/influaudit/backend/internal/influencer"
	"github.com/getnyx/influaudit/backend/internal/llm"
	"github.com/getnyx/influaudit/backend/internal/ml"
	"github.com/getnyx/influaudit/backend/internal/oauth"
	"github.com/getnyx/influaudit/backend/internal/platform/errs"
	"github.com/getnyx/influaudit/backend/internal/platform/storage"
	reportport "github.com/getnyx/influaudit/backend/internal/report/port"
	"github.com/getnyx/influaudit/backend/internal/scoring"
)

// This file is the audit orchestrator's wiring: the thin adapters that project
// each real module onto the consumer-side ports the audit module declares in
// internal/audit/port. The orchestrator imports none of these modules; every
// arrow into it passes through an adapter here. Keeping them in one file makes
// the whole cross-module surface of the audit reviewable at a glance.

// --- Quota: billing.Module -> port.Quota ---------------------------------

// auditQuota adapts the billing module's string-typed quota surface onto the
// audit orchestrator's Quota port, translating between the two opaque
// reservation-id representations.
type auditQuota struct{ b *billing.Module }

func (q auditQuota) Reserve(ctx context.Context, userID uuid.UUID, unit string) (port.ReservationID, error) {
	id, err := q.b.ReserveAudit(ctx, userID, unit)
	return port.ReservationID(id), err
}

func (q auditQuota) Commit(ctx context.Context, id port.ReservationID) error {
	return q.b.CommitReservation(ctx, string(id))
}

func (q auditQuota) Release(ctx context.Context, id port.ReservationID) error {
	return q.b.ReleaseReservation(ctx, string(id))
}

// --- Scorer: scoring.Module -> port.Scorer -------------------------------

// auditScorer adapts scoring.Module.Score onto the Scorer port, translating the
// fraud signal between the two identically-shaped types and narrowing the full
// persisted score down to the summary the orchestrator threads onward.
type auditScorer struct{ s *scoring.Module }

func (a auditScorer) Score(ctx context.Context, auditJobID, influencerID uuid.UUID, snapshots []connector.Snapshot, fraud port.FraudInput) (port.ScoreResult, error) {
	sc, err := a.s.Score(ctx, auditJobID, influencerID, snapshots, scoring.FraudInput{
		Present:           fraud.Present,
		FakeFollowerRate:  fraud.FakeFollowerRate,
		BotCommentRate:    fraud.BotCommentRate,
		EngagementAnomaly: fraud.EngagementAnomaly,
		Confidence:        fraud.Confidence,
		ModelVersion:      fraud.ModelVersion,
	})
	if err != nil {
		return port.ScoreResult{}, err
	}
	return port.ScoreResult{
		Overall:               sc.Overall,
		ContributingPlatforms: sc.ContributingPlatforms,
	}, nil
}

// --- FraudClient: ml.Client -> port.FraudClient --------------------------

// auditFraud adapts the ML client onto the FraudClient port. It runs two ML
// models per snapshot and collapses them into the port's small summary:
//
//   - the per-account fraud model (follower trajectory + posting influence)
//     yields the fake-follower / engagement-anomaly estimate, and
//   - the co-commenter clique model yields the coordination (bot-comment) rate
//     from the share of commenters sitting inside a coordinated clique.
//
// Across platforms it takes the worst-case (maximum) rate per signal, so a clean
// platform never masks a dirty one, and averages the confidences that had real
// evidence behind them. Either model failing degrades that one signal to absent
// rather than failing the audit — the caller already treats a fully-absent fraud
// pass as advisory.
type auditFraud struct{ c *ml.Client }

func (a auditFraud) ScoreFraud(ctx context.Context, snapshots []connector.Snapshot) (port.FraudSummary, error) {
	var (
		out       port.FraudSummary
		confs     []float64
		versions  []string
		anySignal bool
	)

	for _, snap := range snapshots {
		if fr, err := a.c.ScoreFraud(ctx, ml.BuildFraudRequest(snap)); err == nil {
			anySignal = true
			out.FakeFollowerRate = maxF(out.FakeFollowerRate, fr.Score/100)
			out.EngagementAnomaly = maxF(out.EngagementAnomaly, signalValue(fr.Signals, "engagement"))
			confs = append(confs, fr.Confidence)
			versions = appendUnique(versions, fr.ModelVersion)
		}

		// The clique model only has signal when the snapshot carried sampled
		// comments; with none it returns a zero clique fraction, which correctly
		// contributes no coordination evidence.
		if len(snap.Comments) > 0 {
			if pr, err := a.c.DetectPods(ctx, ml.BuildPodsRequest(snap)); err == nil {
				anySignal = true
				out.BotCommentRate = maxF(out.BotCommentRate, pr.CliqueMembershipFraction)
				// Surface the coordination signals for the deliverable's headline and
				// the persisted fraud row: the raw count of maximal cliques (primary)
				// and the membership fraction (secondary). Worst-case across platforms,
				// so a clean platform never masks a coordinated one.
				out.CliqueCount = maxI(out.CliqueCount, pr.CliqueCount)
				out.CliqueMembershipFraction = maxF(out.CliqueMembershipFraction, pr.CliqueMembershipFraction)
				confs = append(confs, pr.Confidence)
				versions = appendUnique(versions, pr.ModelVersion)
			}
		}
	}

	if !anySignal {
		return port.FraudSummary{Present: false}, nil
	}
	out.Present = true
	out.Confidence = meanF(confs)
	out.ModelVersion = strings.Join(versions, "+")
	return out, nil
}

// --- Reporter: llm.Module (+ scoring read) -> port.Reporter --------------

// auditReporter adapts the llm module onto the Reporter port. The narrow
// ReportInput the orchestrator supplies carries only the composite score and the
// raw snapshots, so the adapter reconstructs the rich advisory input: it reads
// the full score the scorer just persisted (niche, tier, per-dimension
// breakdown, benchmark provenance) and aggregates reach and handle from the
// snapshots. The llm module records the generation's cost on an llm_generation
// row and returns its id, which becomes the port's report handle.
type auditReporter struct {
	llm     *llm.Module
	scoring *scoring.Module
}

func (r auditReporter) GenerateReport(ctx context.Context, in port.ReportInput) (port.ReportOutput, port.Usage, error) {
	view, err := r.scoring.ReportView(ctx, in.InfluencerID)
	if err != nil {
		return port.ReportOutput{}, port.Usage{}, err
	}

	subscores := make([]llm.Subscore, 0, len(view.Subscores))
	for _, s := range view.Subscores {
		subscores = append(subscores, llm.Subscore{Name: s.Name, Value: s.Value, Confidence: s.Confidence})
	}

	platforms := make([]string, 0, len(in.Score.ContributingPlatforms))
	for _, p := range in.Score.ContributingPlatforms {
		platforms = append(platforms, string(p))
	}

	_, usage, genID, err := r.llm.GenerateReport(ctx, llm.ReportInput{
		AuditJobID:     in.AuditJobID,
		Purpose:        "summary",
		Handle:         primaryHandle(in.Snapshots),
		Niche:          view.Niche,
		Tier:           view.Tier,
		Followers:      totalFollowers(in.Snapshots),
		Platforms:      platforms,
		InfluenceScore: in.Score.Overall,
		Authenticity:   view.Authenticity,
		Subscores:      subscores,
		Fraud: llm.FraudEstimate{
			Confidence: in.Fraud.Confidence,
			Estimate:   true,
		},
		Metrics:        supportingMetrics(in.Snapshots),
		BenchmarkLabel: view.BenchmarkLabel,
	})
	if err != nil {
		return port.ReportOutput{}, port.Usage{}, err
	}

	return port.ReportOutput{GenerationID: genID}, port.Usage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		CostMicros:   usage.CostMicros,
		Cached:       usage.Cached,
	}, nil
}

// --- Connections: influencer + oauth -> port.Connections -----------------

// auditConnections adapts the influencer and oauth modules onto the Connections
// port. This is the one cross-cutting seam: the influencer module owns the
// profile→owner→handles path, while the oauth module holds the encrypted tokens
// keyed by owner. The adapter joins them so the audit orchestrator sees a single
// list of connected platforms, each with its live token.
//
// A profile with no owner (a brand vetting a creator it holds no OAuth grant
// for) yields handles with nil tokens: the correct cold-start, where a connector
// falls back to its public, API-key path (YouTube today) rather than failing.
type auditConnections struct {
	influencer *influencer.Module
	oauth      *oauth.Module
}

func (a auditConnections) ListConnections(ctx context.Context, influencerID uuid.UUID) ([]port.Connection, error) {
	profile, err := a.influencer.AuditProfileOf(ctx, influencerID)
	if err != nil {
		return nil, err
	}

	// Index the owner's live connections by platform, when there is an owner. A
	// profile with no owner keeps an empty index, so every handle resolves to a
	// nil token. Each entry carries both the token and the id the provider
	// resolved at connect time (e.g. the numeric Instagram Business account id).
	type liveConn struct {
		token     connector.OAuthToken
		accountID string
	}
	live := map[connector.Platform]liveConn{}
	if profile.OwnerUserID != nil {
		conns, err := a.oauth.LiveConnections(ctx, *profile.OwnerUserID)
		if err != nil {
			return nil, err
		}
		for _, lc := range conns {
			live[connector.Platform(lc.Platform)] = liveConn{token: lc.Token, accountID: lc.ProviderAccountID}
		}
	}

	conns := make([]port.Connection, 0, len(profile.Handles))
	for _, h := range profile.Handles {
		conn := port.Connection{
			Platform:  h.Platform,
			Handle:    h.Handle,
			AccountID: h.AccountID,
		}
		if lc, ok := live[h.Platform]; ok {
			t := lc.token
			conn.Token = &t
			conn.AccountID = connAccountID(h.AccountID, lc.accountID)
		}
		conns = append(conns, conn)
	}
	return conns, nil
}

// connAccountID chooses the account id a connected-platform fetch runs against.
// For a connected platform the OAuth-resolved id is authoritative — it is the id
// the live API must be queried with (e.g. the numeric Instagram Business account
// id, not the public handle) — so it wins whenever the provider resolved one.
// A provider that resolved nothing leaves the handle's own id in place, which is
// the correct cold-start/public-path behavior.
func connAccountID(handleID, liveID string) string {
	if liveID != "" {
		return liveID
	}
	return handleID
}

// --- CallerID: auth -> port.CallerID -------------------------------------

// auditCaller adapts the auth module's context accessor onto the CallerID port,
// so the audit module never imports auth. It mirrors identityFromAuth; the two
// exist separately because each port is owned by a different consumer.
type auditCaller struct{}

func (auditCaller) CallerID(ctx context.Context) (uuid.UUID, error) {
	id, ok := auth.UserID(ctx)
	if !ok {
		return uuid.Nil, errs.New(errs.KindUnauthorized, "app.unauthenticated",
			"this endpoint requires authentication")
	}
	return id, nil
}

// --- Report module ports -------------------------------------------------

// reportAuditReader adapts audit.Module.AuditView onto the report module's
// AuditReader port. The audit read is already caller-scoped, so authorization is
// inherited: a caller can only render reports for their own audits.
type reportAuditReader struct{ a *audit.Module }

func (r reportAuditReader) AuditView(ctx context.Context, auditID string) (reportport.AuditView, error) {
	v, err := r.a.AuditView(ctx, auditID)
	if err != nil {
		return reportport.AuditView{}, err
	}
	return reportport.AuditView{
		ID:           v.ID,
		InfluencerID: v.InfluencerID,
		Status:       v.Status,
		Platforms:    v.Platforms,
		RequestedAt:  v.RequestedAt,
		FinishedAt:   v.FinishedAt,
	}, nil
}

// reportScoreReader adapts scoring.Module.ReportView onto the report module's
// ScoreReader port. A not-found score (a fully failed audit, or an influencer
// never scored) is not an error to the report: it is disclosed as an absent
// score, so the read still succeeds and the deliverable says so.
type reportScoreReader struct{ s *scoring.Module }

func (r reportScoreReader) ScoreOf(ctx context.Context, influencerID uuid.UUID) (reportport.ScoreView, error) {
	view, err := r.s.ReportView(ctx, influencerID)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return reportport.ScoreView{Present: false}, nil
		}
		return reportport.ScoreView{}, err
	}
	out := reportport.ScoreView{
		Present:          true,
		Overall:          view.Overall,
		Authenticity:     view.Authenticity,
		Niche:            view.Niche,
		Tier:             view.Tier,
		BenchmarkLabel:   view.BenchmarkLabel,
		VerificationTier: view.VerificationTier,
		Subscores:        make([]reportport.Subscore, 0, len(view.Subscores)),
	}
	for _, s := range view.Subscores {
		out.Subscores = append(out.Subscores, reportport.Subscore{Name: s.Name, Value: s.Value, Confidence: s.Confidence})
	}
	return out, nil
}

// reportNarrativeReader adapts llm.Module.NarrativeOf onto the report module's
// NarrativeReader port, mapping the "no stored narrative" bool into the port's
// Present flag.
type reportNarrativeReader struct{ l *llm.Module }

func (r reportNarrativeReader) NarrativeOf(ctx context.Context, auditJobID uuid.UUID) (reportport.Narrative, error) {
	out, ok, err := r.l.NarrativeOf(ctx, auditJobID)
	if err != nil {
		return reportport.Narrative{}, err
	}
	if !ok {
		return reportport.Narrative{Present: false}, nil
	}
	nar := reportport.Narrative{
		Present:    true,
		Summary:    out.Summary,
		GrowthTips: out.GrowthTips,
		BrandFit:   out.BrandFit,
	}
	for _, wf := range out.WeaknessFixPairs {
		nar.WeaknessFixPairs = append(nar.WeaknessFixPairs, reportport.WeaknessFix{Weakness: wf.Weakness, Fix: wf.Fix})
	}
	return nar, nil
}

// reportFraudReader adapts audit.Module.FraudResultOf onto the report module's
// FraudReader port. A job with no stored fraud row (a failed audit, or one that
// never reached the fraud step) is disclosed as Found=false, not an error, so
// the report still renders and simply omits the coordination headline.
type reportFraudReader struct{ a *audit.Module }

func (r reportFraudReader) FraudOf(ctx context.Context, auditJobID uuid.UUID) (reportport.FraudView, error) {
	fr, found, err := r.a.FraudResultOf(ctx, auditJobID)
	if err != nil {
		return reportport.FraudView{}, err
	}
	if !found {
		return reportport.FraudView{Found: false}, nil
	}
	return reportport.FraudView{
		Found:                    true,
		Present:                  fr.Present,
		FakeFollowerRate:         fr.FakeFollowerRate,
		BotCommentRate:           fr.BotCommentRate,
		EngagementAnomaly:        fr.EngagementAnomaly,
		CliqueCount:              fr.CliqueCount,
		CliqueMembershipFraction: fr.CliqueMembershipFraction,
		Confidence:               fr.Confidence,
		ModelVersion:             fr.ModelVersion,
	}, nil
}

// --- Storage: platform/storage -> reportport.Storage --------------------

// reportStorage adapts the platform S3 client onto the report module's Storage
// port. The client may be nil when object storage is unconfigured (a dev machine
// with no S3): rather than fail the boot, the methods then return an unavailable
// error at call time, so the rest of the API works and only the publish/badge
// path reports the missing dependency — the same degrade-at-call-time posture the
// llm module takes for an absent API key.
type reportStorage struct{ s *storage.Client }

func (r reportStorage) Put(ctx context.Context, key, contentType string, data []byte) error {
	if r.s == nil {
		return errs.New(errs.KindUnavailable, "app.storage_unconfigured", "object storage is not configured")
	}
	_, err := r.s.PutObject(ctx, key, contentType, data)
	return err
}

func (r reportStorage) ShareURL(key string, ttl time.Duration) (string, error) {
	if r.s == nil {
		return "", errs.New(errs.KindUnavailable, "app.storage_unconfigured", "object storage is not configured")
	}
	return r.s.PresignGetURL(key, ttl)
}

// --- small numeric / snapshot helpers ------------------------------------

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func meanF(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func appendUnique(xs []string, v string) []string {
	if v == "" {
		return xs
	}
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// signalValue returns the value of the first signal whose name contains sub, or
// 0 when none matches. It lets the fraud adapter pull one named component (the
// engagement-deviation signal) out of the model's explainable signal list
// without hard-coding its exact name.
func signalValue(signals []ml.SignalContribution, sub string) float64 {
	for _, s := range signals {
		if strings.Contains(s.Name, sub) {
			return s.Value
		}
	}
	return 0
}

// primaryHandle returns the handle of the first snapshot that carried one, used
// only to address the report.
func primaryHandle(snaps []connector.Snapshot) string {
	for _, s := range snaps {
		if s.Handle != "" {
			return s.Handle
		}
	}
	return ""
}

// totalFollowers sums follower counts across the snapshots that produced data,
// the aggregate reach the report cites.
func totalFollowers(snaps []connector.Snapshot) int64 {
	var total int64
	for _, s := range snaps {
		total += s.Followers
	}
	return total
}

// supportingMetrics projects each snapshot's follower count into the report's
// supporting-figure list, labelled by platform. It is intentionally conservative:
// only figures already present on the snapshot are surfaced, never derived ones
// the model might mistake for measured values.
func supportingMetrics(snaps []connector.Snapshot) []llm.Metric {
	metrics := make([]llm.Metric, 0, len(snaps))
	for _, s := range snaps {
		metrics = append(metrics, llm.Metric{
			Name:  string(s.Platform) + "_followers",
			Value: float64(s.Followers),
		})
	}
	return metrics
}

// httpDoerForML is the HTTP client the ml client uses. It is defined here so the
// audit wiring owns the one place the backend reaches the ML service.
var httpDoerForML = &http.Client{Timeout: connectorHTTPTimeout}

// httpDoerForPDF is the HTTP client the Gotenberg PDF renderer uses. Chromium
// rendering is heavier than a JSON call, so it gets a longer deadline than a
// connector or ML request.
var httpDoerForPDF = &http.Client{Timeout: 60 * time.Second}
