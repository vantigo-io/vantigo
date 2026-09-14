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
});
