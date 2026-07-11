import "server-only";
import type {
  CostDashboardResponse,
  DisputeResponse,
  QueueMonitorResponse,
  ResolveDisputeRequest,
} from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/** GET /admin/disputes — the review queue (open disputes). Admin only: the
 * backend returns 403 for a non-admin caller. */
export function listDisputeQueue(token: string): Promise<DisputeResponse[]> {
  return backendFetch<DisputeResponse[]>("/admin/disputes", { token });
}

/** POST /admin/disputes/{id}/resolve — record an admin decision (the labelling
 * act of the ML loop). */
export function resolveDispute(
  id: string,
  body: ResolveDisputeRequest,
  token: string,
): Promise<DisputeResponse> {
  return backendFetch<DisputeResponse>(
    `/admin/disputes/${encodeURIComponent(id)}/resolve`,
    { method: "POST", body, token },
  );
}

/** GET /admin/costs — the LLM API-cost dashboard aggregate. */
export function getCostDashboard(
  token: string,
): Promise<CostDashboardResponse> {
  return backendFetch<CostDashboardResponse>("/admin/costs", { token });
}

/** GET /admin/queues — live asynq queue state (the job monitor). */
export function getQueueMonitor(token: string): Promise<QueueMonitorResponse> {
  return backendFetch<QueueMonitorResponse>("/admin/queues", { token });
}
