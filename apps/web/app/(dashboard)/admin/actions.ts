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
 * the audit's fraud flag (a false positive); `rejected` lets it stand. The
 * decision is the supervised label the training export later carries. On success
 * it revalidates the admin page so the queue reflects the removal.
 */
export async function resolveDisputeAction(
  id: string,
  decision: "upheld" | "rejected",
  notes: string,
): Promise<ResolveState> {
  const token = await requireToken();
  try {
    await resolveDispute(id, { decision, notes: notes || undefined }, token);
    revalidatePath("/admin");
    return { ok: true };
  } catch (error) {
    if (error instanceof ApiError) {
      return { error: error.message };
    }
    return { error: "Could not resolve the dispute." };
  }
}
