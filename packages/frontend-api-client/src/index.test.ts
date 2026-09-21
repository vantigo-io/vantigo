import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiConflictError, ApiValidationError, createApiClient } from "./index";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("frontend API client", () => {
  it("parses problem details into an ApiError", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(500, {
          error: {
            code: "product_unavailable",
            message: "Product service is unavailable",
            fields: { sku: ["Try again later"] },
          },
        }),
      ),
    );
    const client = createApiClient();

    await expect(client.request("/api/v1/products")).rejects.toMatchObject({
      message: "Product service is unavailable",
      status: 500,
      code: "product_unavailable",
      fields: { sku: ["Try again later"] },
    });
  });

  it("applies a caller-supplied transformUrl", async () => {
    const transformUrl = (url: string) =>
      url.startsWith("/api/") && !url.startsWith("/api/v1/identity/") ? `/custom/prefix${url}` : url;
    const fetchMock = vi.fn().mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApiClient({ transformUrl });

    await expect(client.request("/api/v1/products", { method: "POST" })).resolves.toEqual({ ok: true });

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(["/custom/prefix/api/v1/products"]);
  });

  it("passes business API URLs through unchanged with no transformUrl configured", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApiClient();

    await expect(client.request("/api/v1/customers?page=1", { method: "POST" })).resolves.toEqual({ ok: true });

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(["/api/v1/customers?page=1"]);
  });

  // The cutover removed this package's CSRF apparatus: ensureCsrfToken's
  // preflight against GET /api/v1/identity/antiforgery and the X-XSRF-TOKEN
  // header it set. The Go server has neither — it relies on
  // http.CrossOriginProtection — so the preflight would 404 and reject every
  // mutating request before its business fetch was ever issued. Nothing here
  // pinned that removal: when it was made, only the customers app's suite went
  // red and the four other apps caught nothing. These two tests are the pin,
  // in the package that owns the behaviour.
  it("issues exactly one fetch for a mutating request, with no antiforgery preflight", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApiClient();

    await expect(client.request("/api/v1/customers", { method: "POST" })).resolves.toEqual({ ok: true });

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(["/api/v1/customers"]);
  });

  it("sends no antiforgery token header on a mutating request", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApiClient();

    await client.request("/api/v1/customers", { method: "POST", body: "{}" });

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const headerNames = [...new Headers(init.headers).keys()];
    expect(headerNames.filter((name) => /xsrf|csrf/i.test(name))).toEqual([]);
  });

  describe("ApiConflictError (409 problem JSON with a title)", () => {
    it("carries the same message and status a generic error would have, plus the parsed body", async () => {
      const body = {
        title: "Customer revision conflict",
        detail: "The customer was changed by someone else.",
        status: 409,
      };
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(409, body)));
      const client = createApiClient();

      const error = await client.request("/api/v1/customers/1001").catch((e: unknown) => e);

      expect(error).toBeInstanceOf(ApiConflictError);
      const conflict = error as ApiConflictError;
      // Same values the pre-existing generic path computed for this exact
      // response shape (problemMessage's error.message ?? detail ?? title).
      expect(conflict.message).toBe("The customer was changed by someone else.");
      expect(conflict.status).toBe(409);
      expect(conflict.code).toBeUndefined();
      expect(conflict.problem).toEqual(body);
    });

    it("populates code from the top-level problem field when there is no nested error.code", async () => {
      const body = {
        title: "Duplicate legal identity",
        code: "duplicate_legal_identity",
        detail: "Another customer already has this legal identity.",
        status: 409,
        duplicates: [{ id: 5, customerNumber: 1005, name: "Acme AS", status: "active" }],
      };
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(409, body)));
      const client = createApiClient();

      const error = (await client.request("/api/v1/customers").catch((e: unknown) => e)) as ApiConflictError;

      expect(error).toBeInstanceOf(ApiConflictError);
      expect(error.code).toBe("duplicate_legal_identity");
      expect(error.problem.duplicates).toEqual(body.duplicates);
    });

    it("prefers a nested error.code over a top-level one, as the generic path already did", async () => {
      const body = { title: "Conflict", code: "top_level_code", error: { code: "nested_code" } };
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(409, body)));
      const client = createApiClient();

      const error = (await client.request("/api/v1/x").catch((e: unknown) => e)) as ApiConflictError;

      expect(error.code).toBe("nested_code");
    });

    it("still throws ApiValidationError for a 409 whose body carries fields, not ApiConflictError", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          jsonResponse(409, {
            title: "Invalid customer",
            errors: { name: ["Name is required"] },
          }),
        ),
      );
      const client = createApiClient();

      const error = await client.request("/api/v1/x").catch((e: unknown) => e);

      expect(error).toBeInstanceOf(ApiValidationError);
      expect(error).not.toBeInstanceOf(ApiConflictError);
    });

    it("still throws the generic error for a 409 with no top-level title (nested-error-shaped body)", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          jsonResponse(409, {
            error: { code: "account_exists", message: "An account already exists.", fields: null },
          }),
        ),
      );
      const client = createApiClient();

      const error = await client.request("/api/v1/x").catch((e: unknown) => e);

      expect(error).not.toBeInstanceOf(ApiConflictError);
      expect(error).toMatchObject({ status: 409, code: "account_exists", message: "An account already exists." });
    });

    it("still throws the generic error for a 409 with an unparsable body", async () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 409 })));
      const client = createApiClient();

      const error = await client.request("/api/v1/x").catch((e: unknown) => e);

      expect(error).not.toBeInstanceOf(ApiConflictError);
      expect(error).toMatchObject({ status: 409 });
    });
  });
});
