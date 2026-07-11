"use client";

import { useState, useTransition } from "react";
import {
  resolveDisputeAction,
  type ResolveState,
} from "@/app/(dashboard)/admin/actions";

/**
 * Inline resolver for one open dispute. Uphold clears the audit's fraud flag (a
 * false positive); Reject lets it stand. Both submit the optional notes as the
 * admin's justification. The call runs as a Server Action so the Bearer token
 * stays server-side; on success the admin page revalidates and the row drops out
 * of the queue.
 */
export function ResolveDisputeForm({ disputeId }: { disputeId: string }) {
  const [notes, setNotes] = useState("");
  const [state, setState] = useState<ResolveState>({});
  const [pending, startTransition] = useTransition();

  function resolve(decision: "upheld" | "rejected") {
    startTransition(async () => {
      setState(await resolveDisputeAction(disputeId, decision, notes));
    });
  }

  return (
    <div className="mt-3 flex flex-col gap-2">
      <textarea
        value={notes}
        onChange={(e) => setNotes(e.target.value)}
        placeholder="Resolution notes (optional)"
        rows={2}
        className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
      />
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => resolve("upheld")}
          disabled={pending}
          className="rounded-md border border-[var(--line)] px-3 py-1 text-sm font-medium hover:bg-[var(--surface-2)] disabled:opacity-60"
        >
          Uphold (clear flag)
        </button>
        <button
          type="button"
          onClick={() => resolve("rejected")}
          disabled={pending}
          className="rounded-md border border-[var(--line)] px-3 py-1 text-sm font-medium hover:bg-[var(--surface-2)] disabled:opacity-60"
        >
          Reject (flag stands)
        </button>
        {pending && (
          <span className="text-sm text-[var(--muted)]">Resolving…</span>
        )}
      </div>
      {state.error && (
        <p className="text-sm text-[var(--color-critical)]">{state.error}</p>
      )}
    </div>
  );
}
