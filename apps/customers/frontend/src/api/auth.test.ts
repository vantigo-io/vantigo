import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { fetchSession, signIn, signOut } from "./auth";
import { clearCsrfToken, getCsrfToken, setUnauthorizedHandler } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const session = { user: { id: "1", displayName: "Ada Lovelace", email: "ada@example.com", roles: ["user"] } };

describe("auth api", () => {
  afterEach(() => {
    clearCsrfToken();
    setUnauthorizedHandler(undefined);
  });

  it("rotates the CSRF token after a successful login", async () => {
    const fetchMock = stubFetch(
      (url: RequestInfo | URL) => {
        if (String(url) === "/auth/login") return Promise.resolve(jsonResponse(200, session));
        return Promise.resolve(jsonResponse(200, { token: "unused" }));
      },
      { csrfTokens: ["before-login", "after-login"] },
    );

    await expect(signIn("ada@example.com", "correct horse battery staple")).resolves.toEqual(session);

    expect(fetchMock.calls.map(([url]) => url)).toEqual(["/auth/antiforgery", "/auth/login", "/auth/antiforgery"]);
    expect(getCsrfToken()).toBe("after-login");
  });

  it("treats session and credential 401s as expected control flow", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    const fetchMock = stubFetch(
      (url: RequestInfo | URL) => {
        if (String(url) === "/auth/antiforgery") return Promise.resolve(jsonResponse(200, { token: "login-token" }));
        return Promise.resolve(new Response(null, { status: 401 }));
      },
      { session: "delegate" },
    );

    await expect(fetchSession()).resolves.toBeNull();
    await expect(signIn("ada@example.com", "wrong password")).rejects.toMatchObject({ status: 401 });
    expect(unauthorized).not.toHaveBeenCalled();
    expect(fetchMock.calls.map(([url]) => url)).toEqual(["/auth/session", "/auth/antiforgery", "/auth/login"]);
  });

  it("clears the CSRF token even when sign-out fails", async () => {
    const fetchMock = stubFetch((url: RequestInfo | URL) => {
      if (String(url) === "/auth/antiforgery") return Promise.resolve(jsonResponse(200, { token: "logout-token" }));
      return Promise.resolve(new Response(null, { status: 500 }));
    });

    await expect(signOut()).rejects.toMatchObject({ status: 500 });
    expect(getCsrfToken()).toBeNull();
    expect(fetchMock.calls.map(([url]) => url)).toEqual(["/auth/antiforgery", "/auth/logout"]);
  });
});
