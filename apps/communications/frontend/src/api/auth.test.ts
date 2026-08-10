import { describe, expect, it, vi } from "vitest";
import { bootstrapAccount, signIn } from "./auth";
import { clearCsrfToken, ensureCsrfToken, request } from "./request";

const session = { user: { id: "owner-1", displayName: "Owner", email: "owner@example.com", roles: ["Owner"] } };

describe("auth CSRF lifecycle", () => {
  it("refreshes the token after sign-in succeeds", async () => {
    clearCsrfToken();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: "anonymous-token" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(session), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: "authenticated-token" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await signIn("owner@example.com", "password");
    await request("/api/v1/identity/logout", { method: "POST" });

    expect(new Headers(fetchMock.mock.calls[1][1]?.headers).get("X-XSRF-TOKEN")).toBe("anonymous-token");
    expect(new Headers(fetchMock.mock.calls[3][1]?.headers).get("X-XSRF-TOKEN")).toBe("authenticated-token");
  });

  it("refreshes the token after bootstrap succeeds", async () => {
    clearCsrfToken();
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: "setup-token" }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(session), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: "owner-token" }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await bootstrapAccount({
      secret: "setup-secret",
      email: "owner@example.com",
      displayName: "Owner",
      password: "password",
    });
    await ensureCsrfToken();

    expect(fetchMock).toHaveBeenNthCalledWith(3, "/api/v1/identity/antiforgery", { credentials: "include" });
  });
});
