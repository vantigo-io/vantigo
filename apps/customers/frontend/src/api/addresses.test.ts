import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  ApiValidationError,
  createCustomerAddress,
  customerAddressesQueryOptions,
  deleteCustomerAddress,
  makeAddressPrimary,
  updateCustomerAddress,
} from "./addresses";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const address = {
  id: 1,
  type: "invoice",
  label: null,
  line1: "Storgata 1",
  line2: null,
  postalCode: "0150",
  city: "Oslo",
  region: null,
  country: "no",
  isPrimary: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

describe("customerAddressesQueryOptions", () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keys the query ["customers", id, "addresses"], the key every address mutation invalidates', () => {
    expect(customerAddressesQueryOptions(1001).queryKey).toEqual(["customers", 1001, "addresses"]);
  });

  it("GETs the customer's addresses and unwraps the data envelope", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { data: [address] }));
    stubFetch(fetchMock);

    const options = customerAddressesQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual([address]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses", expect.anything());
  });
});

describe("createCustomerAddress", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("POSTs the full address body", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, address));
    stubFetch(fetchMock);

    const input = {
      type: "invoice",
      label: null,
      line1: "Storgata 1",
      line2: null,
      postalCode: "0150",
      city: "Oslo",
      region: null,
      country: "no",
      isPrimary: false,
    };
    const result = await createCustomerAddress(1001, input);

    expect(result).toEqual(address);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });

  it("throws ApiValidationError keyed by field, including the 50-address cap", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(400, {
          title: "Invalid address",
          status: 400,
          errors: { addresses: ["A customer can have at most 50 addresses"] },
        }),
      ),
    );

    const error = await createCustomerAddress(1001, {
      type: "invoice",
      label: null,
      line1: "Storgata 1",
      line2: null,
      postalCode: "0150",
      city: "Oslo",
      region: null,
      country: "no",
      isPrimary: false,
    }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({
      addresses: "A customer can have at most 50 addresses",
    });
  });
});

describe("updateCustomerAddress", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("PUTs the full address body to the address's own URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, address));
    stubFetch(fetchMock);

    const input = {
      type: "invoice",
      label: "Head office",
      line1: "Storgata 1",
      line2: null,
      postalCode: "0150",
      city: "Oslo",
      region: null,
      country: "no",
      isPrimary: true,
    };
    await updateCustomerAddress(1001, 1, input);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    });
  });
});

describe("makeAddressPrimary", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the full address back with isPrimary set to true, nothing else changed", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, { ...address, isPrimary: true }));
    stubFetch(fetchMock);
    const notYetPrimary = { ...address, isPrimary: false };

    await makeAddressPrimary(1001, notYetPrimary);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        type: "invoice",
        label: null,
        line1: "Storgata 1",
        line2: null,
        postalCode: "0150",
        city: "Oslo",
        region: null,
        country: "no",
        isPrimary: true,
      }),
    });
  });
});

describe("deleteCustomerAddress", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("DELETEs the address's own URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    stubFetch(fetchMock);

    await deleteCustomerAddress(1001, 1);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/addresses/1", { method: "DELETE" });
  });
});
