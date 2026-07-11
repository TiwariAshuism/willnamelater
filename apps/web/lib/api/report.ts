import "server-only";
import type { Report } from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/** GET /audits/{id}/report — the assembled report (score + fraud + narrative). */
export function getReport(auditId: string, token: string): Promise<Report> {
  return backendFetch<Report>(
    `/audits/${encodeURIComponent(auditId)}/report`,
    { token },
  );
}
