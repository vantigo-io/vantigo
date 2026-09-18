import { describe, expect, it } from "vitest";
import { stubFetch } from "../test/fetch";
import { serviceVariantSearchQueryOptions } from "./products";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

const productPage = {
  data: [
    {
      id: 1,
      name: "Prosjektledelse",
      type: "Service",
      variants: [
        { id: 11, sku: "PM-SENIOR", unit: "hour" },
        { id: 12, sku: "PM-JUNIOR", unit: "hour" },
      ],
    },
    {
      id: 2,
      name: "Kabel 2,5mm",
      type: "Good",
      variants: [{ id: 21, sku: "CABLE-25", unit: "metre" }],
    },
  ],
  pagination: { page: 1, pageSize: 20, totalCount: 2, totalPages: 1 },
};

describe("serviceVariantSearchQueryOptions", () => {
  it("flattens service products to their variants and drops everything else", async () => {
    const fetchMock = stubFetch(() => Promise.resolve(jsonResponse(200, productPage)));

    const options = serviceVariantSearchQueryOptions(" pm ");
    const result = await runQuery(options);

    expect(result).toEqual([
      { variantId: 11, productName: "Prosjektledelse", sku: "PM-SENIOR", unit: "hour" },
      { variantId: 12, productName: "Prosjektledelse", sku: "PM-JUNIOR", unit: "hour" },
    ]);
    expect(options.queryKey).toEqual(["projects", "service-variants", "pm"]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/products?page=1&pageSize=20&search=pm");
  });

  it("lists the first page of services when nothing has been typed", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, pageSize: 20 } })),
    );

    const result = await runQuery(serviceVariantSearchQueryOptions(""));

    expect(result).toEqual([]);
    expect(fetchMock.actualCalls[0]?.[0]).toBe("/api/v1/products?page=1&pageSize=20");
  });

  it("skips a service product that has no variants", async () => {
    stubFetch(() =>
      Promise.resolve(
        jsonResponse(200, { data: [{ id: 3, name: "Rådgivning", type: "Service", variants: [] }], pagination: {} }),
      ),
    );

    await expect(runQuery(serviceVariantSearchQueryOptions("råd"))).resolves.toEqual([]);
  });
});
