import { NextResponse } from "next/server";
import { cookies } from "next/headers";
import { signupStart } from "@/lib/api/signup";
import { sessionCookieSecure } from "@/lib/env";
import { ApiError } from "@/lib/api/http";
import { OAUTH_STATE_COOKIE } from "@/app/api/oauth/[provider]/start/route";

/**
 * POST /api/oauth/signup/start   body: { email }
 *
 * Begins the anonymous OAuth-as-signup grant: hands the captured email to the
 * backend, stashes the returned anti-CSRF `state` in a short-lived HttpOnly
 * cookie for the callback to verify, and returns the Meta consent URL for the
 * browser to navigate to. The JWT never touches browser JS — the account is
 * minted server-side on the callback.
 */
export async function POST(request: Request): Promise<NextResponse> {
  let email = "";
  try {
    const body = (await request.json()) as { email?: string };
    email = (body.email ?? "").trim();
  } catch {
    return NextResponse.json(
      { code: "invalid_body", message: "expected a JSON body with an email" },
      { status: 400 },
    );
  }
  if (!email) {
    return NextResponse.json(
      { code: "email_required", message: "an email is required to continue" },
      { status: 400 },
    );
  }

  try {
    const { authorization_url, state } = await signupStart(email);
    const store = await cookies();
    store.set(OAUTH_STATE_COOKIE, state, {
      httpOnly: true,
      secure: sessionCookieSecure(),
      sameSite: "lax",
      path: "/",
      maxAge: 600,
    });
    return NextResponse.json({ authorization_url });
  } catch (error) {
    const code = error instanceof ApiError ? error.code : "signup_start_failed";
    const message =
      error instanceof ApiError ? error.message : "could not start the connection";
    const status = error instanceof ApiError ? error.status : 502;
    return NextResponse.json({ code, message }, { status });
  }
}
