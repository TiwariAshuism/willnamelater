"use client";

import { useActionState } from "react";
import {
  runAuditAction,
  type RunAuditState,
} from "@/app/(dashboard)/audits/actions";
import { Button } from "@/components/ui/Button";
import type { InfluencerResponse } from "@influaudit/contracts";

const initialState: RunAuditState = {};

const PLATFORM_OPTIONS = [{ value: "youtube", label: "YouTube" }];

export function RunAuditForm({
  influencers,
  defaultInfluencerId,
}: {
  influencers: InfluencerResponse[];
  defaultInfluencerId?: string;
}) {
  const [state, action, pending] = useActionState(runAuditAction, initialState);

  return (
    <form action={action} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <label
          htmlFor="influencer_id"
          className="text-sm font-medium text-[var(--ink-secondary)]"
        >
          Influencer
        </label>
        <select
          id="influencer_id"
          name="influencer_id"
          defaultValue={defaultInfluencerId ?? ""}
          required
          className="rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-sm"
        >
          <option value="" disabled>
            Select an influencer…
          </option>
          {influencers.map((inf) => (
            <option key={inf.id} value={inf.id}>
              {inf.display_name ?? inf.id}
            </option>
          ))}
        </select>
      </div>

      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium text-[var(--ink-secondary)]">
          Platforms
        </legend>
        {PLATFORM_OPTIONS.map((opt) => (
          <label key={opt.value} className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              name="platforms"
              value={opt.value}
              defaultChecked
            />
            {opt.label}
          </label>
        ))}
      </fieldset>

      {state.error && (
        <p role="alert" className="text-sm text-[var(--color-critical)]">
          {state.error}
        </p>
      )}

      <Button type="submit" disabled={pending || influencers.length === 0}>
        {pending ? "Submitting…" : "Run audit"}
      </Button>
    </form>
  );
}
