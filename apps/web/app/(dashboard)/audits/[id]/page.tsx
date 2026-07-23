import Link from "next/link";
import { notFound } from "next/navigation";
import { requireToken } from "@/lib/auth";
import { getAudit } from "@/lib/api/audits";
import { getReport } from "@/lib/api/report";
import { getScoreHistory, getLatestScore } from "@/lib/api/scoring";
import { getProfileSummary, type ProfileSummary } from "@/lib/api/profile";
import { ApiError } from "@/lib/api/http";
import { Card, CardTitle } from "@/components/ui/Card";
import { AuditStatusBadge } from "@/components/audits/AuditStatusBadge";
import { AuditAutoRefresh } from "@/components/audits/AuditAutoRefresh";
import { ScoreTrendChart } from "@/components/audits/ScoreTrendChart";
import { ReportView } from "@/components/audits/ReportView";
import { DownloadPdfButton } from "@/components/audits/DownloadPdfButton";
import { ShareReport } from "@/components/audits/ShareReport";
import { FactorCards } from "@/components/funnel/FactorCards";
import { AudienceSnapshot } from "@/components/funnel/AudienceSnapshot";
import { VerifiedMetricsStrip } from "@/components/funnel/VerifiedMetricsStrip";
import { ReadinessMeter } from "@/components/funnel/ReadinessMeter";
import { MediaKitCta } from "@/components/funnel/MediaKitCta";
import { TrackView } from "@/components/funnel/TrackView";
import type { Report, ScorePoint, ScoreResponse } from "@influaudit/contracts";

const PENDING = new Set(["queued", "running"]);
const HAS_RESULT = new Set(["partial", "succeeded"]);

export default async function AuditDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const token = await requireToken();

  const audit = await getAudit(id, token).catch((error) => {
    if (error instanceof ApiError && error.status === 404) notFound();
    throw error;
  });

  const isPending = PENDING.has(audit.status);
  const resultReady = HAS_RESULT.has(audit.status);

  // Report + trend are only meaningful once the audit produced data. Fetch them
  // best-effort so a still-running or failed audit renders without crashing.
  let report: Report | null = null;
  let history: ScorePoint[] = [];
  let score: ScoreResponse | null = null;
  let profile: ProfileSummary | null = null;
  if (resultReady) {
    const influencerId = audit.influencer_id;
    const noInfluencer = () => Promise.reject(new Error("no influencer id"));
    const [reportResult, historyResult, scoreResult, profileResult] =
      await Promise.allSettled([
        getReport(id, token),
        influencerId ? getScoreHistory(influencerId, token) : noInfluencer(),
        influencerId ? getLatestScore(influencerId, token) : noInfluencer(),
        influencerId ? getProfileSummary(influencerId, token) : noInfluencer(),
      ]);
    if (reportResult.status === "fulfilled") report = reportResult.value;
    if (historyResult.status === "fulfilled") {
      history = historyResult.value.points ?? [];
    }
    if (scoreResult.status === "fulfilled") score = scoreResult.value;
    if (profileResult.status === "fulfilled") profile = profileResult.value;
  }

  return (
    <div className="flex flex-col gap-6">
      <AuditAutoRefresh active={isPending} />

      <div className="flex items-start justify-between gap-4">
        <div>
          <Link
            href="/audits"
            className="text-sm text-[var(--color-brand)] underline"
          >
            ← All audits
          </Link>
          <h1 className="mt-2 flex items-center gap-3 text-xl font-semibold">
            Audit {audit.id.slice(0, 8)}
            <AuditStatusBadge status={audit.status} />
          </h1>
          <p className="mt-1 text-sm text-[var(--muted)]">
            Requested {new Date(audit.requested_at).toLocaleString()}
          </p>
        </div>
        {report && audit.status !== "failed" && (
          <DownloadPdfButton auditId={audit.id} />
        )}
      </div>

      {audit.error_message && (
        <p className="rounded-md border border-[var(--line)] bg-[var(--surface-2)] px-4 py-2 text-sm text-[var(--color-critical)]">
          {audit.error_message}
        </p>
      )}

      {/* Per-platform results */}
      <Card>
        <CardTitle className="mb-4">Platforms</CardTitle>
        <ul className="flex flex-col divide-y divide-[var(--line)]">
          {audit.platforms.map((p) => (
            <li key={p.platform} className="flex items-center justify-between py-2">
              <span className="capitalize">{p.platform}</span>
              <AuditStatusBadge status={p.status} />
            </li>
          ))}
          {audit.platforms.length === 0 && (
            <li className="py-2 text-sm text-[var(--ink-secondary)]">
              {isPending
                ? "Collecting platform data…"
                : "No platform results."}
            </li>
          )}
        </ul>
      </Card>

      {/* Score trend */}
      {resultReady && (
        <Card>
          <CardTitle className="mb-4">Score trend</CardTitle>
          <ScoreTrendChart points={history} />
        </Card>
      )}

      {/* Already-captured data surfaced for the result page (PRD §8): the verified
          headline strip, the audience snapshot, and the media-kit readiness meter.
          Every value here is measured or explicitly "not measured" — never a 0. */}
      {profile && (
        <>
          <Card>
            <CardTitle className="mb-1">Verified metrics</CardTitle>
            <p className="mb-4 text-sm text-[var(--muted)]">
              Pulled straight from your connected account. A metric we could not
              measure shows &ldquo;not measured&rdquo; — never a zero.
            </p>
            <VerifiedMetricsStrip strip={profile.metrics_strip} />
          </Card>

          <Card>
            <CardTitle className="mb-4">Audience snapshot</CardTitle>
            <AudienceSnapshot audience={profile.audience} />
          </Card>

          <Card>
            <ReadinessMeter readiness={profile.readiness} />
          </Card>
        </>
      )}

      {/* Per-factor breakdown with plain-language improvement lines (PRD §8). */}
      {score && score.factors && score.factors.length > 0 && (
        <Card>
          <TrackView
            event="score_shown"
            extras={{
              influencer_id: audit.influencer_id,
              audit_job_id: audit.id,
            }}
          />
          <CardTitle className="mb-1">Your four factors</CardTitle>
          <p className="mb-4 text-sm text-[var(--muted)]">
            How brand-ready you are, factor by factor. Bands are relative to your
            follower tier and are directional in v1.
            {score.engagement_rate_band &&
              score.engagement_rate_band !== "not_assessed" &&
              ` Engagement rate: ${score.engagement_rate_band.replace(
                /_/g,
                " ",
              )}.`}
          </p>
          <FactorCards factors={score.factors} />
        </Card>
      )}

      {/* Full report */}
      {report && <ReportView report={report} />}

      {/* Publish a shareable public badge (a scored report only). */}
      {report && report.score?.available && (
        <Card>
          <CardTitle className="mb-3">Share</CardTitle>
          <p className="mb-3 text-sm text-[var(--muted)]">
            Publish a public badge others can view without signing in — the score
            and its context only, never your private recommendations.
          </p>
          <ShareReport auditId={audit.id} />
        </Card>
      )}

      {/* Phase-2 seam: measure media-kit demand (story F1/F3). */}
      {report && report.score?.available && <MediaKitCta />}

      {isPending && (
        <p className="text-sm text-[var(--ink-secondary)]">
          This audit is still running — results appear here automatically.
        </p>
      )}
    </div>
  );
}
