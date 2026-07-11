"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

/**
 * While an audit is still `queued`/`running`, re-fetch the Server Component
 * tree on an interval so the status and results update without a manual reload.
 */
export function AuditAutoRefresh({
  active,
  intervalMs = 4000,
}: {
  active: boolean;
  intervalMs?: number;
}) {
  const router = useRouter();

  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => router.refresh(), intervalMs);
    return () => clearInterval(id);
  }, [active, intervalMs, router]);

  return null;
}
