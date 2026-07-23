"use client";

/**
 * Client-side funnel instrumentation. Fires a first-party event to this app's own
 * /api/events route (which forwards to the backend), so the browser never sees
 * API_BASE_URL. Best-effort and non-blocking: it prefers sendBeacon (survives a
 * navigation away, e.g. a redirect to the OAuth consent screen) and swallows every
 * failure — instrumentation must never break the page that fired it.
 */
export interface TrackExtras {
  public_slug?: string;
  influencer_id?: string;
  audit_job_id?: string;
}

export function track(eventType: string, extras?: TrackExtras): void {
  if (typeof window === "undefined" || !eventType) return;
  try {
    const payload = JSON.stringify({ event_type: eventType, ...extras });
    if (typeof navigator !== "undefined" && navigator.sendBeacon) {
      navigator.sendBeacon(
        "/api/events",
        new Blob([payload], { type: "application/json" }),
      );
      return;
    }
    void fetch("/api/events", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: payload,
      keepalive: true,
    });
  } catch {
    // Best-effort: never let a tracking failure surface.
  }
}
