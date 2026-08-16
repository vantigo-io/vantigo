import { afterEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "./index";

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
});
