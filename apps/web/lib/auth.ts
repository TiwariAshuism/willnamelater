import "server-only";
import { cache } from "react";
import { redirect } from "next/navigation";
import type { UserResponse } from "@influaudit/contracts";
import { getAccessToken } from "@/lib/session";
import { me } from "@/lib/api/auth";

/**
 * Server-Component-side session access. Token refresh happens earlier, in
 * `middleware.ts`, so by the time a Server Component renders the access cookie
 * is already fresh — these helpers only read it.
 */

/** The access token, or redirect to /login if there is none. */
export async function requireToken(): Promise<string> {
  const token = await getAccessToken();
  if (!token) {
    redirect("/login");
  }
  return token;
}

/** The authenticated user, deduped per request. Redirects to /login when the
 * token is missing or rejected. */
export const getCurrentUser = cache(async (): Promise<UserResponse> => {
  const token = await requireToken();
  return me(token);
});
