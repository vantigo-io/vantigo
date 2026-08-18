import { afterEach, describe, expect, it, vi } from "vitest";
import { createApiClient, setActiveTenantSlug, setTenantRoutingEnabled, tenantAwareUrl } from "./index";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

afterEach(() => {
  vi.unstubAllGlobals();
  setTenantRoutingEnabled(false);
  setActiveTenantSlug(undefined);
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

  it("acquires and reuses a CSRF token for unsafe requests", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { token: "csrf-token" }))
      .mockResolvedValueOnce(jsonResponse(200, { saved: true }))
      .mockResolvedValueOnce(jsonResponse(200, { saved: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApiClient();

    await expect(client.request("/api/v1/products", { method: "POST" })).resolves.toEqual({ saved: true });
    await expect(client.request("/api/v1/products", { method: "PUT" })).resolves.toEqual({ saved: true });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/identity/antiforgery", { credentials: "include" });
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get("X-XSRF-TOKEN")).toBe("csrf-token");
    expect(new Headers(fetchMock.mock.calls[2]?.[1]?.headers).get("X-XSRF-TOKEN")).toBe("csrf-token");
  });

  it("supports tenant-aware URL transformation without routing identity calls", async () => {
    const tenantSlug = "acme west";
    const transformUrl = (url: string) =>
      url.startsWith("/api/") && !url.startsWith("/api/v1/identity/")
        ? `/api/v1/t/${encodeURIComponent(tenantSlug)}${url.slice(7)}`
        : url;
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, { token: "csrf-token" }))
      .mockResolvedValueOnce(jsonResponse(200, { ok: true }));
    vi.stubGlobal("fetch", fetchMock);
    const client = createApiClient({ transformUrl });

    await expect(client.request("/api/v1/products", { method: "POST" })).resolves.toEqual({ ok: true });

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      "/api/v1/identity/antiforgery",
      "/api/v1/t/acme%20west/products",
    ]);
  });

  describe("shared tenant routing", () => {
    it("prefixes business API calls for every client once enabled", async () => {
      setTenantRoutingEnabled(true);
      setActiveTenantSlug("default");
      const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true }));
      vi.stubGlobal("fetch", fetchMock);
      // A module client created without any tenant transform of its own.
      const moduleClient = createApiClient();

      await expect(moduleClient.request("/api/v1/customers?page=1")).resolves.toEqual({ ok: true });

      expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/t/default/customers?page=1");
    });

    it("keeps identity endpoints global and is inert when disabled or unset", () => {
      setTenantRoutingEnabled(true);
      setActiveTenantSlug("default");
      expect(tenantAwareUrl("/api/v1/identity/session")).toBe("/api/v1/identity/session");
      expect(tenantAwareUrl("/api/v1/t/default/customers")).toBe("/api/v1/t/default/customers");
      expect(tenantAwareUrl("/health")).toBe("/health");

      setActiveTenantSlug(undefined);
      expect(tenantAwareUrl("/api/v1/customers")).toBe("/api/v1/customers");

      setActiveTenantSlug("default");
      setTenantRoutingEnabled(false);
      expect(tenantAwareUrl("/api/v1/customers")).toBe("/api/v1/customers");
    });

    it("encodes the tenant slug in the prefix", () => {
      setTenantRoutingEnabled(true);
      setActiveTenantSlug("acme west");
      expect(tenantAwareUrl("/api/v1/customers")).toBe("/api/v1/t/acme%20west/customers");
    });
  });
});
