import "server-only";
import type { Report, PublishResult, PublicBadge } from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/** GET /audits/{id}/report — the assembled report (score + fraud + narrative). */
export function getReport(auditId: string, token: string): Promise<Report> {
  return backendFetch<Report>(
    `/audits/${encodeURIComponent(auditId)}/report`,
    { token },
  );
}

/** POST /audits/{id}/report/publish — render + store the PDF and mint the durable
 * public badge. Returns the stable slug and a presigned, expiring PDF link. */
export function publishReport(
  auditId: string,
  token: string,
): Promise<PublishResult> {
  return backendFetch<PublishResult>(
    `/audits/${encodeURIComponent(auditId)}/report/publish`,
    { method: "POST", token },
  );
}

/** GET /reports/{slug} — the PUBLIC badge projection. Unauthenticated by design:
 * a badge is a shareable credential, so no token is attached. */
export function getPublicBadge(slug: string): Promise<PublicBadge> {
  return backendFetch<PublicBadge>(`/reports/${encodeURIComponent(slug)}`);
}
