import { NextResponse } from "next/server";
import { logout } from "@/lib/api/auth";
import {
  clearSession,
  getAccessToken,
  getRefreshToken,
} from "@/lib/session";

/**
 * POST /api/auth/logout
 *
 * Best-effort revokes the refresh token on the backend, then clears every
 * session cookie regardless of the backend outcome.
 */
export async function POST(): Promise<NextResponse> {
  const token = await getAccessToken();
  const refreshToken = await getRefreshToken();

  if (token && refreshToken) {
    try {
      await logout({ refresh_token: refreshToken }, token);
    } catch {
      // Revocation is best-effort; we still clear local cookies below.
    }
  }

  await clearSession();
  return NextResponse.json({ ok: true });
}
