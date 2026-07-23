import "server-only";

/**
 * Server-only configuration. Reading any of these from a Client Component is a
 * build error thanks to the `server-only` import above — which is the point:
 * the backend URL and cookie policy must never be bundled into browser JS.
 */

function required(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(
      `Missing required environment variable: ${name}. See apps/web/.env.example.`,
    );
  }
  return value;
}

/** Backend base URL, including the `/v1` base path (e.g. http://localhost:8080/v1). */
export function apiBaseUrl(): string {
  // Trim a trailing slash so callers can always join with a leading-slash path.
  return required("API_BASE_URL").replace(/\/+$/, "");
}

/** Public base URL of this web app, used to build OAuth redirect URIs. */
export function appBaseUrl(): string {
  return required("APP_BASE_URL").replace(/\/+$/, "");
}

/** Whether session cookies should be marked `Secure` (HTTPS-only). */
export function sessionCookieSecure(): boolean {
  return process.env.SESSION_COOKIE_SECURE === "true";
}
