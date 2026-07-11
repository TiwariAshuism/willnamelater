import { requireToken } from "@/lib/auth";
import {
  listDisputeQueue,
  getCostDashboard,
  getQueueMonitor,
} from "@/lib/api/admin";
import { ApiError } from "@/lib/api/http";
import { Card, CardTitle } from "@/components/ui/Card";
import { ResolveDisputeForm } from "@/components/admin/ResolveDisputeForm";
import type {
  CostDashboardResponse,
  DisputeResponse,
  QueueMonitorResponse,
} from "@influaudit/contracts";

export const metadata = { title: "InfluAudit — Admin" };

function usd(n: number): string {
  return `$${n.toFixed(2)}`;
}

export default async function AdminPage() {
  const token = await requireToken();

  // The dispute queue read gates the page: it is admin-only, so a 401/403 here
  // means this caller is not an admin and the rest is not worth fetching.
  let disputes: DisputeResponse[];
  try {
    disputes = await listDisputeQueue(token);
  } catch (error) {
    if (
      error instanceof ApiError &&
      (error.status === 403 || error.status === 401)
    ) {
      return (
        <Card>
          <CardTitle className="mb-2">Admin</CardTitle>
          <p className="text-sm text-[var(--muted)]">
            This area requires an admin account.
          </p>
        </Card>
      );
    }
    throw error;
  }

  const [costsResult, queuesResult] = await Promise.allSettled([
    getCostDashboard(token),
    getQueueMonitor(token),
  ]);
  const costs: CostDashboardResponse | null =
    costsResult.status === "fulfilled" ? costsResult.value : null;
  const queues: QueueMonitorResponse | null =
    queuesResult.status === "fulfilled" ? queuesResult.value : null;

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-xl font-semibold">Admin</h1>

      {/* Dispute review queue — the ML labelling loop. */}
      <Card>
        <CardTitle className="mb-4">
          Dispute queue{" "}
          <span className="text-sm font-normal text-[var(--muted)]">
            ({disputes.length} open)
          </span>
        </CardTitle>
        {disputes.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">
            No open disputes. Resolved disputes feed the training-label export.
          </p>
        ) : (
          <ul className="flex flex-col divide-y divide-[var(--line)]">
            {disputes.map((d) => (
              <li key={d.id} className="py-4">
                <div className="flex items-start justify-between gap-4">
                  <div>
                    <p className="text-sm font-medium">
                      Audit {d.audit_job_id.slice(0, 8)}
                    </p>
                    <p className="mt-1 text-sm text-[var(--muted)]">
                      {d.reason}
                    </p>
                    <p className="mt-1 text-xs text-[var(--muted)]">
                      Filed {new Date(d.created_at).toLocaleString()}
                    </p>
                  </div>
                </div>
                <ResolveDisputeForm disputeId={d.id} />
              </li>
            ))}
          </ul>
        )}
      </Card>

      {/* LLM API-cost dashboard. */}
      <Card>
        <CardTitle className="mb-4">LLM cost</CardTitle>
        {costs ? (
          <>
            <div className="flex flex-wrap gap-8">
              <Stat label="Total spend" value={usd(costs.total_cost_usd)} />
              <Stat
                label="Generations"
                value={costs.total_generations.toLocaleString()}
              />
              <Stat
                label="Cache hit rate"
                value={`${Math.round(costs.cache_hit_rate * 100)}%`}
              />
            </div>
            {costs.by_model.length > 0 && (
              <table className="mt-4 w-full text-sm">
                <thead>
                  <tr className="text-left text-[var(--muted)]">
                    <th className="py-1 font-medium">Model</th>
                    <th className="py-1 font-medium">Gens</th>
                    <th className="py-1 font-medium">Cached</th>
                    <th className="py-1 font-medium">Cost</th>
                  </tr>
                </thead>
                <tbody>
                  {costs.by_model.map((m) => (
                    <tr key={m.model} className="border-t border-[var(--line)]">
                      <td className="py-1">{m.model}</td>
                      <td className="py-1">{m.generations}</td>
                      <td className="py-1">{m.cached_generations}</td>
                      <td className="py-1">{usd(m.cost_usd)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        ) : (
          <p className="text-sm text-[var(--muted)]">Cost data unavailable.</p>
        )}
      </Card>

      {/* asynq job monitor. */}
      <Card>
        <CardTitle className="mb-4">Job queues</CardTitle>
        {queues && queues.queues.length > 0 ? (
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-[var(--muted)]">
                <th className="py-1 font-medium">Queue</th>
                <th className="py-1 font-medium">Pending</th>
                <th className="py-1 font-medium">Active</th>
                <th className="py-1 font-medium">Retry</th>
                <th className="py-1 font-medium">Failed</th>
                <th className="py-1 font-medium">Latency</th>
              </tr>
            </thead>
            <tbody>
              {queues.queues.map((q) => (
                <tr key={q.queue} className="border-t border-[var(--line)]">
                  <td className="py-1">{q.queue}</td>
                  <td className="py-1">{q.pending}</td>
                  <td className="py-1">{q.active}</td>
                  <td className="py-1">{q.retry}</td>
                  <td className="py-1">{q.failed}</td>
                  <td className="py-1">{q.latency_ms} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p className="text-sm text-[var(--muted)]">
            No active queues (the worker may be idle or unreachable).
          </p>
        )}
      </Card>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-[var(--muted)]">
        {label}
      </div>
      <div className="text-2xl font-semibold">{value}</div>
    </div>
  );
}
