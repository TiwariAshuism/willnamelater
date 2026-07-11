import Link from "next/link";
import { notFound } from "next/navigation";
import { requireToken } from "@/lib/auth";
import { getAudit } from "@/lib/api/audits";
import { getReport } from "@/lib/api/report";
import { getScoreHistory } from "@/lib/api/scoring";
import { ApiError } from "@/lib/api/http";
import { Card, CardTitle } from "@/components/ui/Card";
import { AuditStatusBadge } from "@/components/audits/AuditStatusBadge";
import { AuditAutoRefresh } from "@/components/audits/AuditAutoRefresh";
import { ScoreTrendChart } from "@/components/audits/ScoreTrendChart";
import { ReportView } from "@/components/audits/ReportView";
import { DownloadPdfButton } from "@/components/audits/DownloadPdfButton";
import { ShareReport } from "@/components/audits/ShareReport";
import type { Report, ScorePoint } from "@influaudit/contracts";

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
  if (resultReady) {
    const influencerId = audit.influencer_id;
    const [reportResult, historyResult] = await Promise.allSettled([
      getReport(id, token),
      influencerId
        ? getScoreHistory(influencerId, token)
        : Promise.reject(new Error("no influencer id")),
    ]);
    if (reportResult.status === "fulfilled") report = reportResult.value;
    if (historyResult.status === "fulfilled") {
      history = historyResult.value.points ?? [];
    }
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

      {isPending && (
        <p className="text-sm text-[var(--ink-secondary)]">
          This audit is still running — results appear here automatically.
        </p>
      )}
    </div>
  );
}
