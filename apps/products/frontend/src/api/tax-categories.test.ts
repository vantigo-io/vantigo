import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { createTaxCategory, deleteTaxCategory, taxCategoriesQueryOptions, updateTaxCategory } from "./tax-categories";

const endpoint = "/api/v1/products/tax-categories";
const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const input = { name: "Standard", kind: "Standard" as const, rate: 0.25 };

describe("tax categories api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("lists tax categories with a stable query key and response mapping", async () => {
    const categories = [{ id: 1, ...input }];
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, categories));
    stubFetch(fetchMock);

    const options = taxCategoriesQueryOptions();
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual(categories);
    expect(options.queryKey).toEqual(["tax-categories"]);
    expect(fetchMock).toHaveBeenCalledWith(endpoint, { signal: undefined });
  });

  it("creates, updates, and deletes tax categories with the expected payloads", async () => {
    const created = { id: 1, ...input };
    const updatedInput = { name: "Reduced", kind: "Reduced" as const, rate: 0.15 };
    const updated = { id: 1, ...updatedInput };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(201, created))
      .mockResolvedValueOnce(jsonResponse(200, updated))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    stubFetch(fetchMock);

    await expect(createTaxCategory(input)).resolves.toEqual(created);
    await expect(updateTaxCategory(1, updatedInput)).resolves.toEqual(updated);
    await expect(deleteTaxCategory(1)).resolves.toBeUndefined();

    expect(fetchMock.mock.calls.map(([url, init]) => [url, init?.method, init?.body])).toEqual([
      [endpoint, "POST", JSON.stringify(input)],
      [`${endpoint}/1`, "PUT", JSON.stringify(updatedInput)],
      [`${endpoint}/1`, "DELETE", undefined],
    ]);
  });
});
