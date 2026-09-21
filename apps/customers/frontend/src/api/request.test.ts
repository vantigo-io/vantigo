import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { sessionQueryKey } from "./auth";
import { request, setAuthStateClearer, setUnauthorizedHandler } from "./request";

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

  it("sends the session cookie on an unsafe request", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, { saved: true })));

    await expect(
      request<{ saved: boolean }>("/api/v1/customers", {
        method: "POST",
        body: JSON.stringify({ name: "Acme" }),
      }),
    ).resolves.toEqual({ saved: true });

    expect(fetchMock.actualCalls).toHaveLength(1);
    const [url, init] = fetchMock.actualCalls[0];
    expect(url).toBe("/api/v1/customers");
    expect(init?.credentials).toBe("include");
  });

  it("invokes the centralized unauthorized handler for a 401", async () => {
    const unauthorized = vi.fn();
    const clearState = vi.fn();
    setAuthStateClearer(clearState);
    setUnauthorizedHandler(unauthorized);
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })));

    await expect(request("/api/v1/customers")).rejects.toMatchObject({ status: 401 });
    expect(clearState).toHaveBeenCalledOnce();
    expect(unauthorized).toHaveBeenCalledOnce();
  });

  it("allows the auth-state clearer to remove the session cache before redirect", async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(sessionQueryKey, { user: { id: "expired" } });
    setAuthStateClearer(() => queryClient.removeQueries({ queryKey: sessionQueryKey, exact: true }));
    setUnauthorizedHandler(vi.fn());
    stubFetch(() => Promise.resolve(new Response(null, { status: 401 })));

    await expect(request("/api/v1/customers")).rejects.toMatchObject({ status: 401 });

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
    [{ title: "Invalid customer", errors: { name: ["Name is required"] } }],
    [{ error: { message: "Invalid customer", fields: { "identity.id": ["Invalid id"] } } }],
  ])("preserves validation fields for RFC/problem payload %j", async (problem) => {
    stubFetch(() => Promise.resolve(jsonResponse(400, problem)));

    await expect(request("/api/v1/customers", { method: "POST" })).rejects.toMatchObject({
      name: "ApiValidationError",
      fields: expect.any(Object),
    });
  });

  // ApiConflictError itself — status/message/code/problem mapping for every
  // 409 shape — is `@vantigo/frontend-api-client`'s own to test (this file
  // is a thin re-export of that client, not a second implementation of it).
  // This is the one wiring smoke test that belongs here: that this app's
  // `request` really does surface it, end to end.
  it("surfaces a duplicate-legal-identity 409 as ApiConflictError", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(409, {
          title: "Duplicate legal identity",
          code: "duplicate_legal_identity",
          detail: "Another customer already has this legal identity.",
          status: 409,
          duplicates: [{ id: 5, customerNumber: 1005, name: "Acme AS", status: "active" }],
        }),
      ),
    );

    await expect(request("/api/v1/customers", { method: "POST" })).rejects.toMatchObject({
      name: "ApiConflictError",
      status: 409,
      code: "duplicate_legal_identity",
      problem: { duplicates: [{ id: 5, customerNumber: 1005, name: "Acme AS", status: "active" }] },
    });
  });
});
