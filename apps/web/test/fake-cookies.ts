import { vi } from "vitest";

export interface RecordedCookie {
  name: string;
  value: string;
  options?: Record<string, unknown>;
}

/**
 * An in-memory stand-in for the Next.js cookie store returned by
 * `cookies()` (next/headers). It records writes so tests can assert on the
 * cookie attributes the route handler set — this mocks the framework boundary,
 * NOT the data layer (which still issues real fetch calls).
 */
export function createFakeCookieStore(
  seed: Record<string, string> = {},
) {
  const store = new Map<string, string>(Object.entries(seed));
  const sets: RecordedCookie[] = [];
  const deletes: string[] = [];

  const api = {
    get(name: string) {
      return store.has(name) ? { name, value: store.get(name)! } : undefined;
    },
    set(name: string, value: string, options?: Record<string, unknown>) {
      store.set(name, value);
      sets.push({ name, value, options });
    },
    delete(name: string) {
      store.delete(name);
      deletes.push(name);
    },
    has(name: string) {
      return store.has(name);
    },
  };

  return { api, sets, deletes, store };
}

/** Build a `global.fetch` stub that returns a single JSON response. */
export function jsonFetchOnce(body: unknown, status = 200) {
  return vi.fn(async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
}
