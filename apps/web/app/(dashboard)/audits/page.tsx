import Link from "next/link";
import { requireToken } from "@/lib/auth";
import { listAudits } from "@/lib/api/audits";
import { listInfluencers } from "@/lib/api/influencers";
import { Card, CardTitle } from "@/components/ui/Card";
import { RunAuditForm } from "@/components/audits/RunAuditForm";
import { AuditStatusBadge } from "@/components/audits/AuditStatusBadge";
import type { AuditResponse, InfluencerResponse } from "@influaudit/contracts";

export default async function AuditsPage({
  searchParams,
}: {
  searchParams: Promise<{ influencer?: string }>;
}) {
  const token = await requireToken();
  const { influencer } = await searchParams;

  let audits: AuditResponse[] = [];
  let influencers: InfluencerResponse[] = [];
  try {
    [audits, influencers] = await Promise.all([
      listAudits(token),
      listInfluencers(token).then((r) => r.influencers ?? []),
    ]);
  } catch {
    // Fall through to empty state; individual cards note the failure.
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold">Audits</h1>
        <p className="mt-1 text-sm text-[var(--ink-secondary)]">
          Run a new audit or open a past one.
        </p>
      </div>

      <div className="grid gap-6 md:grid-cols-[1fr_2fr]">
        <Card>
          <CardTitle className="mb-4">Run an audit</CardTitle>
          {influencers.length === 0 ? (
            <p className="text-sm text-[var(--ink-secondary)]">
              Add an influencer on the{" "}
              <Link href="/dashboard" className="text-[var(--color-brand)] underline">
                overview
              </Link>{" "}
              first.
            </p>
          ) : (
            <RunAuditForm
              influencers={influencers}
              defaultInfluencerId={influencer}
            />
          )}
        </Card>

        <Card>
          <CardTitle className="mb-4">Your audits</CardTitle>
          {audits.length === 0 && (
            <p className="text-sm text-[var(--ink-secondary)]">
              No audits yet.
            </p>
          )}
          <ul className="flex flex-col divide-y divide-[var(--line)]">
            {audits.map((audit) => (
              <li key={audit.id} className="flex items-center justify-between py-3">
                <div>
                  <Link
                    href={`/audits/${audit.id}`}
                    className="font-medium text-[var(--color-brand)] underline"
                  >
                    {audit.id.slice(0, 8)}
                  </Link>
                  <p className="text-xs text-[var(--muted)]">
                    {audit.requested_platforms.join(", ") || "all platforms"} ·{" "}
                    {new Date(audit.requested_at).toLocaleString()}
                  </p>
                </div>
                <AuditStatusBadge status={audit.status} />
              </li>
            ))}
          </ul>
        </Card>
      </div>
    </div>
  );
}
