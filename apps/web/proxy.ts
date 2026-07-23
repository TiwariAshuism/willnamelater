import { NextResponse, type NextRequest } from "next/server";
import {
  ACCESS_COOKIE,
  REFRESH_COOKIE,
  EXPIRES_COOKIE,
  EXPIRY_SKEW_SECONDS,
} from "@/lib/session-constants";

/**
 * Auth gate + token refresh for the authenticated dashboard.
 *
 * This is the Next.js 16 "Proxy" convention (the former `middleware`). It runs
 * before any protected Server Component renders. Because a Server Component
 * cannot set cookies during render, refresh has to happen here: if the access
 * token is missing/expired but a refresh token exists, we exchange it against
 * the backend and write the new cookies onto the response, so the downstream
 * render reads a fresh token. This module deliberately avoids `next/headers`
 * and `server-only` so it stays runtime-agnostic.
 */

interface AuthTokens {
  access_token: string;
  refresh_token: string;
  expires_in: number;
}

function cookieOptions() {
  return {
    httpOnly: true,
    secure: process.env.SESSION_COOKIE_SECURE === "true",
    sameSite: "lax" as const,
    path: "/",
  };
}

function applyTokens(response: NextResponse, tokens: AuthTokens): void {
  const opts = cookieOptions();
  const expiresAt = Math.floor(Date.now() / 1000) + tokens.expires_in;
  response.cookies.set(ACCESS_COOKIE, tokens.access_token, {
    ...opts,
    maxAge: tokens.expires_in,
  });
  response.cookies.set(EXPIRES_COOKIE, String(expiresAt), {
    ...opts,
    maxAge: tokens.expires_in,
  });
  response.cookies.set(REFRESH_COOKIE, tokens.refresh_token, {
    ...opts,
    maxAge: 60 * 60 * 24 * 30,
  });
}

function clearTokens(response: NextResponse): void {
  response.cookies.delete(ACCESS_COOKIE);
  response.cookies.delete(REFRESH_COOKIE);
  response.cookies.delete(EXPIRES_COOKIE);
}

function isExpired(expiresRaw: string | undefined): boolean {
  if (!expiresRaw) return true;
  const expiresAt = Number(expiresRaw);
  if (!Number.isFinite(expiresAt)) return true;
  return Math.floor(Date.now() / 1000) >= expiresAt - EXPIRY_SKEW_SECONDS;
}

async function refreshTokens(refreshToken: string): Promise<AuthTokens | null> {
  const base = process.env.API_BASE_URL;
  if (!base) return null;
  try {
    const res = await fetch(`${base.replace(/\/+$/, "")}/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ refresh_token: refreshToken }),
    });
    if (!res.ok) return null;
    return (await res.json()) as AuthTokens;
  } catch {
    return null;
  }
}

function redirectToLogin(request: NextRequest): NextResponse {
  const url = new URL("/login", request.url);
  url.searchParams.set("next", request.nextUrl.pathname);
  const response = NextResponse.redirect(url);
  clearTokens(response);
  return response;
}

export async function proxy(request: NextRequest): Promise<NextResponse> {
  const access = request.cookies.get(ACCESS_COOKIE)?.value;
  const refresh = request.cookies.get(REFRESH_COOKIE)?.value;
  const expires = request.cookies.get(EXPIRES_COOKIE)?.value;

  // A valid, unexpired access token: let the request through untouched.
  if (access && !isExpired(expires)) {
    return NextResponse.next();
  }

  // No way to recover a session: bounce to login.
  if (!refresh) {
    return redirectToLogin(request);
  }

  const tokens = await refreshTokens(refresh);
  if (!tokens) {
    return redirectToLogin(request);
  }

  const response = NextResponse.next();
  applyTokens(response, tokens);
  return response;
}

export const config = {
  // Protect the authenticated dashboard surfaces only. Auth pages, API route
  // handlers, and static assets are excluded.
  matcher: ["/dashboard/:path*", "/audits/:path*", "/connections/:path*"],
};
