import "server-only";
import { cookies } from "next/headers";
import type { AuthResponse } from "@influaudit/contracts";
import { sessionCookieSecure } from "@/lib/env";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  EXPIRES_COOKIE,
  EXPIRY_SKEW_SECONDS,
} from "@/lib/session-constants";

/**
 * Server-owned session.
 *
 * The backend hands us a JWT access token (+ refresh token) in an
 * `auth.AuthResponse`. We persist all three cookies as HttpOnly so the JWT is
 * never readable by browser JS — Server Components / route handlers read them
 * back and forward the access token as `Authorization: Bearer`.
 *
 * Three cookies:
 *   ia_session  — the JWT access token (HttpOnly)
 *   ia_refresh  — the refresh token    (HttpOnly)
 *   ia_expires  — access-token expiry, unix seconds (HttpOnly; drives refresh)
 */
export { ACCESS_COOKIE, REFRESH_COOKIE, EXPIRES_COOKIE };

function baseCookieOptions() {
  return {
    httpOnly: true,
    secure: sessionCookieSecure(),
    sameSite: "lax" as const,
    path: "/",
  };
}

/** Persist a fresh set of tokens from a backend auth/refresh response. */
export async function writeSession(auth: AuthResponse): Promise<void> {
  const store = await cookies();
  const opts = baseCookieOptions();
  const expiresAt = Math.floor(Date.now() / 1000) + auth.expires_in;

  // Access + expiry live as long as the access token; the refresh token is
  // longer-lived, so give it a generous maxAge (30 days) rather than tying it
  // to the short access lifetime.
  store.set(ACCESS_COOKIE, auth.access_token, {
    ...opts,
    maxAge: auth.expires_in,
  });
  store.set(EXPIRES_COOKIE, String(expiresAt), {
    ...opts,
    maxAge: auth.expires_in,
  });
  store.set(REFRESH_COOKIE, auth.refresh_token, {
    ...opts,
    maxAge: 60 * 60 * 24 * 30,
  });
}

/** Drop every session cookie (logout / failed refresh). */
export async function clearSession(): Promise<void> {
  const store = await cookies();
  store.delete(ACCESS_COOKIE);
  store.delete(REFRESH_COOKIE);
  store.delete(EXPIRES_COOKIE);
}

/** The current access token, or null if unauthenticated. */
export async function getAccessToken(): Promise<string | null> {
  const store = await cookies();
  return store.get(ACCESS_COOKIE)?.value ?? null;
}

/** The current refresh token, or null. */
export async function getRefreshToken(): Promise<string | null> {
  const store = await cookies();
  return store.get(REFRESH_COOKIE)?.value ?? null;
}

/** True when the stored access token is missing or within the skew of expiry. */
export async function isAccessTokenExpired(): Promise<boolean> {
  const store = await cookies();
  const raw = store.get(EXPIRES_COOKIE)?.value;
  if (!raw) return true;
  const expiresAt = Number(raw);
  if (!Number.isFinite(expiresAt)) return true;
  return Math.floor(Date.now() / 1000) >= expiresAt - EXPIRY_SKEW_SECONDS;
}
