/**
 * Cookie names and timing shared between the Node server layer (`session.ts`,
 * which uses `next/headers`) and the middleware (which runs in a lighter runtime
 * and must not import `next/headers` / `server-only`). Keep this module free of
 * any runtime-specific imports so both can consume it.
 */
export const ACCESS_COOKIE = "ia_session";
export const REFRESH_COOKIE = "ia_refresh";
export const EXPIRES_COOKIE = "ia_expires";

/** Treat a token as "about to expire" this many seconds early, so we refresh
 * before the backend would reject it. */
export const EXPIRY_SKEW_SECONDS = 30;
