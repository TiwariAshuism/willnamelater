import "server-only";
import type {
  AuditResponse,
  SubmitAuditRequest,
} from "@influaudit/contracts";
import { backendFetch, backendFetchRaw } from "@/lib/api/http";

/** GET /audits — the caller's audits, newest first (backend-ordered). */
export function listAudits(token: string): Promise<AuditResponse[]> {
  return backendFetch<AuditResponse[]>("/audits", { token });
}

/** POST /audits — submit a new audit run (idempotent on `idempotency_key`). */
export function submitAudit(
  body: SubmitAuditRequest,
  token: string,
): Promise<AuditResponse> {
  return backendFetch<AuditResponse>("/audits", {
    method: "POST",
    body,
    token,
  });
}

/** GET /audits/{id} — a single audit's status and per-platform result. */
export function getAudit(id: string, token: string): Promise<AuditResponse> {
  return backendFetch<AuditResponse>(`/audits/${encodeURIComponent(id)}`, {
    token,
  });
}

/** GET /audits/{id}/report.pdf — the rendered PDF as a raw Response, so the
 * route handler can stream it straight to the browser. */
export function getReportPdf(id: string, token: string): Promise<Response> {
  return backendFetchRaw(`/audits/${encodeURIComponent(id)}/report.pdf`, {
    token,
  });
}
