import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mockCookies = vi.hoisted(() => {
  const store = new Map<string, string>();
  const deletes: string[] = [];
  return {
    store,
    deletes,
    api: {
      get(name: string) {
        return store.has(name) ? { name, value: store.get(name)! } : undefined;
      },
      set(name: string, value: string) {
        store.set(name, value);
      },
      delete(name: string) {
        store.delete(name);
        deletes.push(name);
      },
    },
  };
});

vi.mock("next/headers", () => ({
  cookies: async () => mockCookies.api,
}));

import { GET } from "./route";

const CONNECTION = {
  connected_at: "2026-07-11T00:00:00Z",
  platform: "youtube",
  provider: "google",
  provider_account_id: "chan-123",
  scopes: ["https://www.googleapis.com/auth/youtube.readonly"],
};

const ctx = { params: Promise.resolve({ provider: "google" }) };

function callbackRequest(query: string): Request {
  return new Request(
    `http://localhost:3000/api/oauth/google/callback${query}`,
  );
}

describe("GET /api/oauth/[provider]/callback", () => {
  beforeEach(() => {
    mockCookies.store.clear();
    mockCookies.deletes.length = 0;
    mockCookies.store.set("ia_oauth_state", "state-xyz");
    mockCookies.store.set("ia_session", "jwt-token");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("exchanges the code and redirects to /connections on success", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(CONNECTION), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await GET(
      callbackRequest("?code=auth-code&state=state-xyz"),
      ctx,
    );

    // The backend exchange carried the code/state and the bearer token.
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(String(url)).toContain(
      "/oauth/google/callback?code=auth-code&state=state-xyz",
    );
    expect(new Headers((init as RequestInit).headers).get("Authorization")).toBe(
      "Bearer jwt-token",
    );

    expect(res.status).toBe(307);
    const location = res.headers.get("location")!;
    expect(location).toContain("/connections");
    expect(location).toContain("connected=google");

    // The single-use state cookie is cleared.
    expect(mockCookies.deletes).toContain("ia_oauth_state");
  });

  it("rejects a state mismatch without calling the backend (CSRF guard)", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");

    const res = await GET(
      callbackRequest("?code=auth-code&state=attacker-state"),
      ctx,
    );

    expect(fetchMock).not.toHaveBeenCalled();
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toContain("error=state_mismatch");
  });

  it("redirects with an error when code/state are missing", async () => {
    const res = await GET(callbackRequest("?state=state-xyz"), ctx);
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toContain(
      "error=missing_code_or_state",
    );
  });
});
