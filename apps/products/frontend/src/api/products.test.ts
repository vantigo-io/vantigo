import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  addProductPrice,
  archiveProduct,
  createProduct,
  productPricesQueryOptions,
  productQueryOptions,
  productsQueryOptions,
  updateProduct,
} from "./products";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const input = { name: "Widget", sku: "W-1", type: "Goods" as const, vatRate: 0.25 };

describe("products api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("lists products with query parameters", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [], pagination: {} }));
    stubFetch(fetchMock);
    const options = productsQueryOptions({ page: 2, pageSize: 10, search: "widget", status: "Active" });
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(options.queryKey).toEqual(["products", { page: 2, pageSize: 10, search: "widget", status: "Active" }]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/products?page=2&pageSize=10&search=widget&status=Active", {
      signal: undefined,
    });
  });

  it("creates, updates, archives, and manages prices", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(201, { id: 1 }))
      .mockResolvedValueOnce(jsonResponse(200, { id: 1, ...input }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(jsonResponse(201, { id: 2, currency: "NOK", amount: 10, validFrom: null, validTo: null }));
    stubFetch(fetchMock);
    await createProduct(input);
    await updateProduct(1, input);
    await archiveProduct(1);
    await addProductPrice(1, { currency: "NOK", amount: 10 });
    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method])).toEqual([
      ["/api/v1/products", "POST"],
      ["/api/v1/products/1", "PUT"],
      ["/api/v1/products/1", "DELETE"],
      ["/api/v1/products/1/prices", "POST"],
    ]);
  });

  it("gets a product and all prices with stable query keys", async () => {
    const product = { id: 1, ...input, status: "Draft", effectivePrices: [] };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, product))
      .mockResolvedValueOnce(jsonResponse(200, []));
    stubFetch(fetchMock);
    await (productQueryOptions(1).queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    await (productPricesQueryOptions(1).queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(productQueryOptions(1).queryKey).toEqual(["products", 1]);
    expect(productPricesQueryOptions(1).queryKey).toEqual(["products", 1, "prices"]);
  });
});
