"use server";

import { revalidatePath } from "next/cache";
import { requireToken } from "@/lib/auth";
import { resolveDispute } from "@/lib/api/admin";
import { ApiError } from "@/lib/api/http";

export interface ResolveState {
  ok?: boolean;
  error?: string;
}

/**
 * Server Action: resolve an open dispute with an admin decision. `upheld` clears
 * the audit's fraud flag (a false positive); `rejected` lets it stand.
 *
 * `labelEvidence` is REQUIRED and is the point of the endpoint: it states what the
 * adjudicator actually OBSERVED, outside the heuristic's own output. A dispute only
 * exists because the heuristic flagged the account, so "the admin agreed with the
 * flag" (`none_reviewed_heuristic_only`) contains no observation the heuristic had
 * not already made — such a decision is still a real outcome for the customer, but
 * it is NEVER exported as a training label. Only the observable kinds can become y.
 * On success it revalidates the admin page so the queue reflects the removal.
 */
export type LabelEvidence =
  | "platform_enforcement_action"
  | "creator_admission"
  | "purchase_receipt_or_panel_invoice"
  | "brand_campaign_conversion_data"
  | "manual_follower_sample_audit"
  | "none_reviewed_heuristic_only";

export async function resolveDisputeAction(
  id: string,
  decision: "upheld" | "rejected",
  notes: string,
  labelEvidence: LabelEvidence,
): Promise<ResolveState> {
  const token = await requireToken();
  try {
    await resolveDispute(
      id,
      { decision, label_evidence: labelEvidence, notes: notes || undefined },
      token,
    );
    revalidatePath("/admin");
    return { ok: true };
  } catch (error) {
    if (error instanceof ApiError) {
      return { error: error.message };
    }
    return { error: "Could not resolve the dispute." };
  }
}
