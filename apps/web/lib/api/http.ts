import "server-only";
import { apiBaseUrl } from "@/lib/env";

/**
 * The single seam between this app and the Go backend. Every data call in the
 * app goes through here — there is no mock layer. Tests exercise this real code
 * path and stub the transport (`global.fetch`) instead of replacing the client.
 */

/** Error thrown for any non-2xx backend response. Mirrors the backend's
 * `{code, message}` error envelope (see services backend `httpx`). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  /** JSON-serializable request body. */
  body?: unknown;
  /** Bearer token to attach as `Authorization`. */
  token?: string | null;
  /** Query-string parameters (undefined/null values are dropped). */
  query?: Record<string, string | number | boolean | undefined | null>;
  /** Forwarded to fetch for cancellation. */
  signal?: AbortSignal;
}

function buildUrl(
  path: string,
  query?: RequestOptions["query"],
): string {
  const url = new URL(apiBaseUrl() + path);
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined && value !== null) {
        url.searchParams.set(key, String(value));
      }
    }
  }
  return url.toString();
}

function headersFor(options: RequestOptions): Headers {
  const headers = new Headers();
  headers.set("Accept", "application/json");
  if (options.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  if (options.token) {
    headers.set("Authorization", `Bearer ${options.token}`);
  }
  return headers;
}

async function toApiError(response: Response): Promise<ApiError> {
  let code = "unknown";
  let message = `Request failed with status ${response.status}`;
  try {
    const data = (await response.json()) as { code?: string; message?: string };
    if (data.code) code = data.code;
    if (data.message) message = data.message;
  } catch {
    // Body was not JSON; keep the status-derived defaults.
  }
  return new ApiError(response.status, code, message);
}

/** Perform a request and return the raw `Response` (used for binary payloads
 * like the report PDF). Throws `ApiError` on a non-2xx status. */
export async function backendFetchRaw(
  path: string,
  options: RequestOptions = {},
): Promise<Response> {
  const response = await fetch(buildUrl(path, options.query), {
    method: options.method ?? "GET",
    headers: headersFor(options),
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    signal: options.signal,
    // Backend responses are per-user; never let Next cache them.
    cache: "no-store",
  });
  if (!response.ok) {
    throw await toApiError(response);
  }
  return response;
}

/** Perform a request and parse a JSON body of type `T`. Returns `undefined`
 * for `204`/empty responses. Throws `ApiError` on a non-2xx status. */
export async function backendFetch<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const response = await backendFetchRaw(path, options);
  if (response.status === 204) {
    return undefined as T;
  }
  const text = await response.text();
  if (text.length === 0) {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}
