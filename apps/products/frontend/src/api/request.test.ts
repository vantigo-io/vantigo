import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { sessionQueryKey } from "./auth";
import { getCsrfToken, request, setAuthStateClearer, setUnauthorizedHandler } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("request", () => {
  afterEach(() => {
    setUnauthorizedHandler(undefined);
    setAuthStateClearer(undefined);
  });

  it("fetches a CSRF token before an unsafe request and sends it on the business request", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { saved: true })));

    await expect(
      request<{ saved: boolean }>("/api/v1/products", {
        method: "POST",
        body: JSON.stringify({ name: "Acme" }),
      }),
    ).resolves.toEqual({ saved: true });

    expect(fetchMock.actualCalls).toHaveLength(1);
    const [url, init] = fetchMock.actualCalls[0];
    expect(url).toBe("/api/v1/products");
    expect(init?.credentials).toBe("include");
    expect(new Headers(init?.headers).get("X-XSRF-TOKEN")).toBe("test-xsrf-token");
  });

  it("invokes the centralized unauthorized handler for a 401", async () => {
    const unauthorized = vi.fn();
    const clearState = vi.fn();
    setAuthStateClearer(clearState);
    setUnauthorizedHandler(unauthorized);
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })));

    await expect(request("/api/v1/products")).rejects.toMatchObject({ status: 401 });
    expect(clearState).toHaveBeenCalledOnce();
    expect(unauthorized).toHaveBeenCalledOnce();
    expect(getCsrfToken()).toBeNull();
  });

  it("allows the auth-state clearer to remove the session cache before redirect", async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(sessionQueryKey, { user: { id: "expired" } });
    setAuthStateClearer(() => queryClient.removeQueries({ queryKey: sessionQueryKey, exact: true }));
    setUnauthorizedHandler(vi.fn());
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })));

    await expect(request("/api/v1/products")).rejects.toMatchObject({ status: 401 });

    expect(queryClient.getQueryData(sessionQueryKey)).toBeUndefined();
  });

  it("keeps expected 401s out of the unauthorized path", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })), { session: "delegate" });

    await expect(
      request("/api/v1/identity/login", { method: "POST", handleUnauthorized: false }),
    ).rejects.toMatchObject({
      status: 401,
    });
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it("keeps session bootstrap 401s out of the global unauthorized path", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })), { session: "delegate" });

    await expect(request("/api/v1/identity/session", { handleUnauthorized: false })).rejects.toMatchObject({
      status: 401,
    });

    expect(unauthorized).not.toHaveBeenCalled();
  });

  it.each([
    [{ title: "Invalid product", errors: { name: ["Name is required"] } }],
    [{ error: { message: "Invalid product", fields: { sku: ["Invalid SKU"] } } }],
  ])("preserves validation fields for RFC/problem payload %j", async (problem) => {
    stubFetch(() => Promise.resolve(jsonResponse(400, problem)));

    await expect(request("/api/v1/products", { method: "POST" })).rejects.toMatchObject({
      name: "ApiValidationError",
      fields: expect.any(Object),
    });
  });
});
