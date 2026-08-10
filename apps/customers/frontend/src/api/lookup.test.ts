import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { fetchBrregLookup } from "./lookup";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("fetchBrregLookup", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("queries by free-text search", async () => {
    const body = { data: [{ legalId: "923609016", legalName: "EQUINOR ASA" }] };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, body));
    stubFetch(fetchMock);

    const result = await fetchBrregLookup({ search: "equinor" });

    expect(result).toEqual(body);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/lookup/brreg?search=equinor", { signal: undefined });
  });

  it("queries by exact legal id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [] }));
    stubFetch(fetchMock);

    await fetchBrregLookup({ legalId: "923609016" });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/lookup/brreg?legalId=923609016", { signal: undefined });
  });

  it("throws on upstream failure", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 502 })));

    await expect(fetchBrregLookup({ search: "equinor" })).rejects.toThrow("Lookup failed (HTTP 502)");
  });
});
