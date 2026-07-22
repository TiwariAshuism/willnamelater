"use client";

import { useEffect } from "react";
import { track, type TrackExtras } from "@/lib/track";

/**
 * Fires one funnel event when the page it sits on first renders in the browser.
 * Renders nothing. Drop it into a Server Component to instrument a page view
 * (landing_view, score_shown, share_open, …) without making the whole page a
 * Client Component.
 */
export function TrackView({
  event,
  extras,
}: {
  event: string;
  extras?: TrackExtras;
}) {
  useEffect(() => {
    track(event, extras);
    // Fire exactly once for this mounted view.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return null;
}
