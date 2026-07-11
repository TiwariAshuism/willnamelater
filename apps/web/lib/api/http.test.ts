import { afterEach, describe, expect, it, vi } from "vitest";
import { backendFetch, ApiError } from "@/lib/api/http";

describe("backendFetch", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("builds the URL against API_BASE_URL and attaches the bearer token", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );

    await backendFetch("/audits", { token: "tok-123" });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("http://backend.test/v1/audits");
    const headers = new Headers((init as RequestInit).headers);
    expect(headers.get("Authorization")).toBe("Bearer tok-123");
  });

  it("serialises a JSON body and sets Content-Type", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response(null, { status: 204 }));

    await backendFetch("/auth/login", {
      method: "POST",
      body: { email: "a@b.com", password: "secret" },
    });

    const [, init] = fetchMock.mock.calls[0]!;
    expect((init as RequestInit).method).toBe("POST");
    expect((init as RequestInit).body).toBe(
      JSON.stringify({ email: "a@b.com", password: "secret" }),
    );
  });

  it("maps a non-2xx {code,message} envelope onto ApiError", async () => {
    // A fresh Response per call: a body can only be consumed once.
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async () =>
        new Response(
          JSON.stringify({ code: "unauthorized", message: "nope" }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
    );

    await expect(backendFetch("/auth/me", { token: "x" })).rejects.toThrowError(
      ApiError,
    );
    await expect(
      backendFetch("/auth/me", { token: "x" }),
    ).rejects.toMatchObject({ status: 401, code: "unauthorized" });
  });
});
