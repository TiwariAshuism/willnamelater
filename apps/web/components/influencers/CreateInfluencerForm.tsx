"use client";

import { useActionState } from "react";
import {
  createInfluencerAction,
  type CreateInfluencerState,
} from "@/app/(dashboard)/dashboard/actions";
import { Button } from "@/components/ui/Button";
import { Field } from "@/components/ui/Field";

const initialState: CreateInfluencerState = {};

export function CreateInfluencerForm() {
  const [state, action, pending] = useActionState(
    createInfluencerAction,
    initialState,
  );

  return (
    <form action={action} className="flex flex-col gap-4">
      <Field label="Display name" name="display_name" type="text" required />
      <div className="grid grid-cols-2 gap-4">
        <Field label="Niche" name="niche" type="text" placeholder="e.g. fitness" />
        <Field label="Country" name="country" type="text" placeholder="e.g. IN" />
      </div>
      {state.error && (
        <p role="alert" className="text-sm text-[var(--color-critical)]">
          {state.error}
        </p>
      )}
      <Button type="submit" disabled={pending}>
        {pending ? "Creating…" : "Add influencer"}
      </Button>
    </form>
  );
}
