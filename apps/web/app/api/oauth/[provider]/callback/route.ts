import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { callback } from "@/lib/api/oauth";
import { getAccessToken } from "@/lib/session";
import { ApiError } from "@/lib/api/http";
import { OAUTH_STATE_COOKIE } from "@/app/api/oauth/[provider]/start/route";

/**
 * GET /api/oauth/{provider}/callback?code=...&state=...
 *
 * The provider redirects the user here after consent. We verify the `state`
 * against the cookie set in `/start` (CSRF), hand the `code`/`state` to the
 * backend to complete the exchange and persist the connection, then redirect
 * the user back into the dashboard.
 *
 * NOTE: for the provider to land here, the backend's OAuth client must have
 * this route registered as its redirect URI
 * (<APP_BASE_URL>/api/oauth/<provider>/callback), or the backend callback must
 * itself redirect back to APP_BASE_URL. Flagged for the backend owner.
 */
export async function GET(
  request: Request,
  ctx: { params: Promise<{ provider: string }> },
): Promise<NextResponse> {
  const { provider } = await ctx.params;
  const requestUrl = new URL(request.url);
  const code = requestUrl.searchParams.get("code");
  const state = requestUrl.searchParams.get("state");
  const providerError = requestUrl.searchParams.get("error");

  const connectionsUrl = new URL("/connections", request.url);

  // The provider itself reported a failure (user denied, etc.).
  if (providerError) {
    connectionsUrl.searchParams.set("error", providerError);
    return NextResponse.redirect(connectionsUrl);
  }

  if (!code || !state) {
    connectionsUrl.searchParams.set("error", "missing_code_or_state");
    return NextResponse.redirect(connectionsUrl);
  }

  const store = await cookies();
  const expectedState = store.get(OAUTH_STATE_COOKIE)?.value;
  if (!expectedState || expectedState !== state) {
    connectionsUrl.searchParams.set("error", "state_mismatch");
    return NextResponse.redirect(connectionsUrl);
  }

  const token = await getAccessToken();
  if (!token) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  try {
    await callback(provider, { code, state }, token);
    store.delete(OAUTH_STATE_COOKIE);
    connectionsUrl.searchParams.set("connected", provider);
    return NextResponse.redirect(connectionsUrl);
  } catch (error) {
    store.delete(OAUTH_STATE_COOKIE);
    connectionsUrl.searchParams.set(
      "error",
      error instanceof ApiError ? error.code : "oauth_callback_failed",
    );
    return NextResponse.redirect(connectionsUrl);
  }
}
