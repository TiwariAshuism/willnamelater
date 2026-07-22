import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { signupCallback } from "@/lib/api/signup";
import { writeSession } from "@/lib/session";
import { ApiError } from "@/lib/api/http";
import { OAUTH_STATE_COOKIE } from "@/app/api/oauth/[provider]/start/route";

/**
 * GET /api/oauth/signup/callback?code=...&state=...
 *
 * Meta redirects the new creator here after consent. We verify `state` against
 * the cookie set in `/signup/start` (CSRF), hand `code`/`state` to the backend
 * which creates the account + connection and mints a session, then write that
 * session as HttpOnly cookies and land the creator on their result.
 *
 * A Meta login with no linked Instagram Business account comes back as a
 * distinct, guided-fix error — we route it to the pre-flight screen with a hint
 * rather than a dead-end failure (the PRD's number-one connect-rate lever).
 */
export async function GET(request: Request): Promise<NextResponse> {
  const url = new URL(request.url);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  const providerError = url.searchParams.get("error");

  const store = await cookies();
  const startUrl = new URL("/start", request.url);

  if (providerError) {
    startUrl.searchParams.set("error", providerError);
    return NextResponse.redirect(startUrl);
  }
  if (!code || !state) {
    startUrl.searchParams.set("error", "missing_code_or_state");
    return NextResponse.redirect(startUrl);
  }

  const expectedState = store.get(OAUTH_STATE_COOKIE)?.value;
  if (!expectedState || expectedState !== state) {
    startUrl.searchParams.set("error", "state_mismatch");
    return NextResponse.redirect(startUrl);
  }

  try {
    const session = await signupCallback({ code, state });
    store.delete(OAUTH_STATE_COOKIE);
    await writeSession(session);
    // The creator is now signed in; land them on their dashboard where the audit
    // runs and the result appears.
    const done = new URL("/dashboard", request.url);
    done.searchParams.set("welcome", "1");
    return NextResponse.redirect(done);
  } catch (error) {
    store.delete(OAUTH_STATE_COOKIE);
    // The one error worth guiding rather than dead-ending: no Instagram Business
    // account linked. Send them back to the pre-flight fix.
    if (
      error instanceof ApiError &&
      error.code === "oauth.instagram_business_account_required"
    ) {
      startUrl.searchParams.set("fix", "business_account");
      return NextResponse.redirect(startUrl);
    }
    startUrl.searchParams.set(
      "error",
      error instanceof ApiError ? error.code : "signup_callback_failed",
    );
    return NextResponse.redirect(startUrl);
  }
}
