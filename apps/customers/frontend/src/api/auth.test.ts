import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { fetchSession, signIn } from "./auth";
import { setUnauthorizedHandler } from "./request";

describe("auth api", () => {
  afterEach(() => {
    setUnauthorizedHandler(undefined);
  });

  it("treats session and credential 401s as expected control flow", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    const fetchMock = stubFetch(() => Promise.resolve(new Response(null, { status: 401 })), { session: "delegate" });

    await expect(fetchSession()).resolves.toBeNull();
    await expect(signIn("ada@example.com", "wrong password")).rejects.toMatchObject({ status: 401 });
    expect(unauthorized).not.toHaveBeenCalled();
    expect(fetchMock.calls.map(([url]) => url)).toEqual(["/api/v1/identity/session", "/api/v1/identity/login"]);
  });
});
