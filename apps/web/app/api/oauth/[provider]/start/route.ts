import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { authorize } from "@/lib/api/oauth";
import { getAccessToken } from "@/lib/session";
import { sessionCookieSecure } from "@/lib/env";
import { ApiError } from "@/lib/api/http";

export const OAUTH_STATE_COOKIE = "ia_oauth_state";

/**
 * GET /api/oauth/{provider}/start
 *
 * Kicks off an OAuth grant: asks the backend for the provider consent URL,
 * stashes the returned `state` in a short-lived HttpOnly cookie for CSRF
 * validation on the callback, then redirects the browser to the provider
 * (e.g. Google for a YouTube connection).
 */
export async function GET(
  _request: Request,
  ctx: { params: Promise<{ provider: string }> },
): Promise<NextResponse> {
  const { provider } = await ctx.params;

  const token = await getAccessToken();
  if (!token) {
    return NextResponse.redirect(new URL("/login", _request.url));
  }

  try {
    const { authorization_url, state } = await authorize(provider, token);

    const store = await cookies();
    store.set(OAUTH_STATE_COOKIE, state, {
      httpOnly: true,
      secure: sessionCookieSecure(),
      sameSite: "lax",
      path: "/",
      maxAge: 600, // 10 minutes to complete the consent screen.
    });

    return NextResponse.redirect(authorization_url);
  } catch (error) {
    const url = new URL("/connections", _request.url);
    url.searchParams.set(
      "error",
      error instanceof ApiError ? error.code : "oauth_start_failed",
    );
    return NextResponse.redirect(url);
  }
}
