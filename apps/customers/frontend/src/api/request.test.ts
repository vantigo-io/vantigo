import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { sessionQueryKey } from "./auth";
import { ApiConflictError, request, setAuthStateClearer, setUnauthorizedHandler } from "./request";

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

  it("throws ApiConflictError for a revision conflict (409, no code, no fields)", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(409, {
          title: "Customer revision conflict",
          detail: "The customer was changed by someone else.",
          status: 409,
        }),
      ),
    );

    const error = await request("/api/v1/customers/1001", { method: "PUT" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiConflictError);
    expect((error as ApiConflictError).status).toBe(409);
    expect((error as ApiConflictError).title).toBe("Customer revision conflict");
    expect((error as ApiConflictError).detail).toBe("The customer was changed by someone else.");
    expect((error as ApiConflictError).code).toBeUndefined();
    expect((error as ApiConflictError).duplicates).toBeUndefined();
    expect((error as ApiConflictError).message).toBe("The customer was changed by someone else.");
  });

  it("throws ApiConflictError carrying code and duplicates for a duplicate legal identity", async () => {
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

    const error = await request("/api/v1/customers", { method: "POST" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiConflictError);
    expect((error as ApiConflictError).code).toBe("duplicate_legal_identity");
    expect((error as ApiConflictError).duplicates).toEqual([
      { id: 5, customerNumber: 1005, name: "Acme AS", status: "active" },
    ]);
  });

  it("still throws the generic error for a 409 with an unparsable body", async () => {
    stubFetch(() => Promise.resolve(new Response(null, { status: 409 })));

    const error = await request("/api/v1/customers/1001/timeline/7", { method: "DELETE" }).catch((e: unknown) => e);

    expect(error).not.toBeInstanceOf(ApiConflictError);
    expect((error as { status?: number }).status).toBe(409);
  });
});
