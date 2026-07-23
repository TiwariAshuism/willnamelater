"use client";

import { useState, type ChangeEvent } from "react";
import { ConnectForm } from "@/components/funnel/ConnectForm";
import { track } from "@/lib/track";

/**
 * The interactive part of the pre-flight screen. The creator confirms they meet
 * the two hard prerequisites (Business/Creator account + linked Facebook Page)
 * before the connect step is revealed, so they reach OAuth already qualified —
 * the single biggest determinant of connect rate. Confirming fires
 * prerequisite_pass so drop-off is instrumented at exactly this step.
 */
export function PreflightGate() {
  const [ready, setReady] = useState(false);

  function onToggle(e: ChangeEvent<HTMLInputElement>) {
    const checked = e.target.checked;
    setReady(checked);
    if (checked) track("prerequisite_pass");
  }

  return (
    <div className="flex flex-col gap-4">
      <label className="flex cursor-pointer items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4 text-sm">
        <input
          type="checkbox"
          checked={ready}
          onChange={onToggle}
          className="mt-0.5 h-4 w-4 accent-[var(--color-brand)]"
        />
        <span>
          My Instagram is a <strong>Business or Creator</strong> account, and it
          is linked to a <strong>Facebook Page</strong>.
        </span>
      </label>

      {ready ? (
        <ConnectForm />
      ) : (
        <p className="text-sm text-[var(--muted)]">
          Confirm the two requirements above to connect. Both are required by
          Instagram before it will share your insights.
        </p>
      )}
    </div>
  );
}
