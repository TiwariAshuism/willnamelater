import { NextResponse } from "next/server";
import { recordEvent } from "@/lib/api/funnel";
import type { AnalyticsIngestRequest } from "@influaudit/contracts";

/**
 * POST /api/events   body: analytics.IngestRequest
 *
 * First-party funnel instrumentation. The browser posts here (never straight to
 * the backend, so API_BASE_URL stays server-only); we forward to the backend
 * ingest. Best-effort: a failure here must never break the page that fired it, so
 * we always answer 204 and swallow errors.
 */
export async function POST(request: Request): Promise<NextResponse> {
  try {
    const body = (await request.json()) as AnalyticsIngestRequest;
    if (body && typeof body.event_type === "string" && body.event_type) {
      await recordEvent(body);
    }
  } catch {
    // Instrumentation is best-effort; never surface an error to the caller.
  }
  return new NextResponse(null, { status: 204 });
}
