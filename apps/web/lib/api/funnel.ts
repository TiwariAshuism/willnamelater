import "server-only";
import type {
  AnalyticsIngestRequest,
  WaitlistCaptureRequest,
} from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/**
 * Public acquisition-funnel writes: first-party analytics events and the
 * email-capture / media-kit waitlist. Both are unauthenticated first-party
 * endpoints; the browser reaches them only through this app's own route handlers
 * (which forward here), so API_BASE_URL stays server-only.
 */

/** POST /events — record one funnel or share-open event. */
export function recordEvent(req: AnalyticsIngestRequest): Promise<void> {
  return backendFetch<void>("/events", { method: "POST", body: req });
}

/** POST /waitlist — capture an email for a named source (idempotent). */
export function captureWaitlist(req: WaitlistCaptureRequest): Promise<void> {
  return backendFetch<void>("/waitlist", { method: "POST", body: req });
}
