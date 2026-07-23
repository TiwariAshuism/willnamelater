"use client";

import { useState, useTransition } from "react";
import {
  resolveDisputeAction,
  type LabelEvidence,
  type ResolveState,
} from "@/app/(dashboard)/admin/actions";

/**
 * What the adjudicator actually OBSERVED, outside the heuristic's own output.
 *
 * This is not paperwork. A dispute exists only because the heuristic flagged the
 * account, so a decision that rests on nothing but the flag is the heuristic
 * agreeing with itself through a human — training on it teaches the model to
 * predict its own opinion. Only the observable kinds below can ever become a
 * training label; "reviewed the flag only" is an honest, first-class answer that
 * resolves the dispute for the customer and is silently excluded from training.
 */
const EVIDENCE_OPTIONS: { value: LabelEvidence; label: string }[] = [
  { value: "none_reviewed_heuristic_only", label: "Reviewed the flag only — no outside evidence" },
  { value: "platform_enforcement_action", label: "Platform took action (takedown / ban / follower removal)" },
  { value: "creator_admission", label: "Creator admitted buying engagement" },
  { value: "purchase_receipt_or_panel_invoice", label: "Purchase receipt or engagement-panel invoice" },
  { value: "brand_campaign_conversion_data", label: "Brand campaign conversion data contradicts the audience" },
  { value: "manual_follower_sample_audit", label: "Manually sampled and inspected the follower list" },
];

/**
 * Inline resolver for one open dispute. Uphold clears the audit's fraud flag (a
 * false positive); Reject lets it stand. Both submit the optional notes as the
 * admin's justification. The call runs as a Server Action so the Bearer token
 * stays server-side; on success the admin page revalidates and the row drops out
 * of the queue.
 */
export function ResolveDisputeForm({ disputeId }: { disputeId: string }) {
  const [notes, setNotes] = useState("");
  // Defaults to the honest common case. It is deliberately NOT a blank that forces
  // a guess: an adjudicator who saw nothing beyond the flag should be able to say
  // exactly that, in one click, without feeling they must claim evidence.
  const [evidence, setEvidence] = useState<LabelEvidence>(
    "none_reviewed_heuristic_only",
  );
  const [state, setState] = useState<ResolveState>({});
  const [pending, startTransition] = useTransition();

  function resolve(decision: "upheld" | "rejected") {
    startTransition(async () => {
      setState(await resolveDisputeAction(disputeId, decision, notes, evidence));
    });
  }

  return (
    <div className="mt-3 flex flex-col gap-2">
      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-[var(--muted)]">
          What did you observe, outside the flag itself?
        </span>
        <select
          value={evidence}
          onChange={(e) => setEvidence(e.target.value as LabelEvidence)}
          className="w-full rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
        >
          {EVIDENCE_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <span className="text-xs text-[var(--muted)]">
          Only decisions backed by an outside observation become training labels.
          &ldquo;Reviewed the flag only&rdquo; still resolves the dispute.
        </span>
      </label>
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
