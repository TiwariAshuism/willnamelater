import "server-only";
import type { AuthorizeResponse, AuthSession } from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/**
 * OAuth-as-signup: the anonymous public funnel where a visitor with no account
 * connects Instagram, and the account is created FROM the Meta grant plus the
 * captured email. Unauthenticated by design — no token is attached.
 */

/** POST /oauth/meta/signup/start — begin the anonymous grant. Carries the
 * captured email in the state; returns the Meta consent URL + anti-CSRF state. */
export function signupStart(email: string): Promise<AuthorizeResponse> {
  return backendFetch<AuthorizeResponse>("/oauth/meta/signup/start", {
    method: "POST",
    body: { email },
  });
}

/** GET /oauth/meta/signup/callback — complete the grant. Creates user +
 * influencer + connection and returns a session (tokens) to sign the new
 * account in. `code`/`state` ride the query string (the OAuth handshake requires
 * them; they are not modelled in the request body). */
export function signupCallback(params: {
  code: string;
  state: string;
}): Promise<AuthSession> {
  return backendFetch<AuthSession>("/oauth/meta/signup/callback", {
    query: { code: params.code, state: params.state },
  });
}
