import { afterEach, describe, expect, it, vi } from "vitest";

// Mock the framework cookie boundary (next/headers). The data layer is NOT
// mocked — the route handler still calls lib/api → global.fetch, which we stub
// as the transport.
const mockCookies = vi.hoisted(() => {
  const store = new Map<string, string>();
  const sets: {
    name: string;
    value: string;
    options?: Record<string, unknown>;
  }[] = [];
  return {
    store,
    sets,
    api: {
      get(name: string) {
        return store.has(name) ? { name, value: store.get(name)! } : undefined;
      },
      set(name: string, value: string, options?: Record<string, unknown>) {
        store.set(name, value);
        sets.push({ name, value, options });
      },
      delete(name: string) {
        store.delete(name);
      },
    },
  };
});

vi.mock("next/headers", () => ({
  cookies: async () => mockCookies.api,
}));

import { POST } from "./route";

const AUTH_RESPONSE = {
  access_token: "jwt-access-token",
  refresh_token: "the-refresh-token",
  expires_in: 900,
  token_type: "Bearer",
  user: {
    id: "user-1",
    email: "creator@example.com",
    email_verified: true,
    role: "influencer",
    created_at: "2026-01-01T00:00:00Z",
  },
};

function loginRequest(body: unknown): Request {
  return new Request("http://localhost:3000/api/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

describe("POST /api/auth/login", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    mockCookies.store.clear();
    mockCookies.sets.length = 0;
  });

  it("stores the JWT in an HttpOnly session cookie and never returns it", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(AUTH_RESPONSE), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const res = await POST(
      loginRequest({ email: "creator@example.com", password: "hunter2xx" }),
    );

    expect(res.status).toBe(200);

    const sessionCookie = mockCookies.sets.find(
      (c) => c.name === "ia_session",
    );
    expect(sessionCookie).toBeDefined();
    expect(sessionCookie!.value).toBe("jwt-access-token");
    expect(sessionCookie!.options).toMatchObject({
      httpOnly: true,
      sameSite: "lax",
      path: "/",
    });

    const refreshCookie = mockCookies.sets.find(
      (c) => c.name === "ia_refresh",
    );
    expect(refreshCookie?.value).toBe("the-refresh-token");
    expect(refreshCookie?.options).toMatchObject({ httpOnly: true });

    // The JWT must not appear in the JSON body handed to the browser.
    const body = await res.json();
    expect(JSON.stringify(body)).not.toContain("jwt-access-token");
    expect(body).toEqual({ user: AUTH_RESPONSE.user });
  });

  it("returns 400 without email/password and does not call the backend", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch");
    const res = await POST(loginRequest({ email: "" }));
    expect(res.status).toBe(400);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("propagates a backend auth error status", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({ code: "invalid_credentials", message: "bad login" }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      ),
    );

    const res = await POST(
      loginRequest({ email: "creator@example.com", password: "wrongpass" }),
    );
    expect(res.status).toBe(401);
    const body = (await res.json()) as { message: string };
    expect(body.message).toBe("bad login");
    expect(mockCookies.sets).toHaveLength(0);
  });
});
