"use server";

import { redirect } from "next/navigation";
import { requireToken } from "@/lib/auth";
import { submitAudit } from "@/lib/api/audits";
import { ApiError } from "@/lib/api/http";

export interface RunAuditState {
  error?: string;
}

/**
 * Server Action: submit an audit for an influencer. Generates the
 * idempotency key server-side so a double submit is a no-op on the backend,
 * then redirects to the new audit's status page.
 */
export async function runAuditAction(
  _prev: RunAuditState,
  formData: FormData,
): Promise<RunAuditState> {
  const token = await requireToken();

  const influencerId = String(formData.get("influencer_id") ?? "").trim();
  if (!influencerId) {
    return { error: "An influencer is required." };
  }
  const requestedPlatforms = formData
    .getAll("platforms")
    .map((value) => String(value))
    .filter(Boolean);

  let auditId: string;
  try {
    const audit = await submitAudit(
      {
        influencer_id: influencerId,
        idempotency_key: crypto.randomUUID(),
        requested_platforms:
          requestedPlatforms.length > 0 ? requestedPlatforms : undefined,
      },
      token,
    );
    auditId = audit.id;
  } catch (error) {
    if (error instanceof ApiError) {
      return { error: error.message };
    }
    return { error: "Could not submit the audit." };
  }

  redirect(`/audits/${auditId}`);
}
