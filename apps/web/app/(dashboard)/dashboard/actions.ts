"use server";

import { revalidatePath } from "next/cache";
import { requireToken } from "@/lib/auth";
import { createInfluencer } from "@/lib/api/influencers";
import { ApiError } from "@/lib/api/http";

export interface CreateInfluencerState {
  error?: string;
}

/**
 * Server Action: create an influencer profile the signed-in user can audit.
 * Calls the real backend; no mock layer.
 */
export async function createInfluencerAction(
  _prev: CreateInfluencerState,
  formData: FormData,
): Promise<CreateInfluencerState> {
  const token = await requireToken();

  const displayName = String(formData.get("display_name") ?? "").trim();
  const niche = String(formData.get("niche") ?? "").trim();
  const country = String(formData.get("country") ?? "").trim();

  if (!displayName) {
    return { error: "Display name is required." };
  }

  try {
    await createInfluencer(
      {
        display_name: displayName,
        niche: niche || null,
        country: country || null,
      },
      token,
    );
  } catch (error) {
    if (error instanceof ApiError) {
      return { error: error.message };
    }
    return { error: "Could not create influencer profile." };
  }

  revalidatePath("/dashboard");
  return {};
}
