import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  ApiValidationError,
  createCustomer,
  customerQueryOptions,
  customersListParams,
  customersQueryOptions,
  legalIdentityQueryOptions,
  NotFoundError,
  updateCustomer,
} from "./customers";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("createCustomer", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("POSTs the name to /api/v1/customers and returns the created id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, { id: 1001 }));
    stubFetch(fetchMock);

    const result = await createCustomer({ name: "Acme" });

    expect(result).toEqual({ id: 1001 });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Acme" }),
    });
  });

  it("throws ApiValidationError with field errors on a 400 validation problem", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(400, {
          title: "Invalid customer",
          status: 400,
          errors: { name: ["A friendly name cannot be null or empty"] },
        }),
      ),
    );

    const error = await createCustomer({ name: "" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({
      name: "A friendly name cannot be null or empty",
    });
  });

  it("throws a generic error on non-validation failures", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 500 })));

    await expect(createCustomer({ name: "Acme" })).rejects.toThrow("Request failed (HTTP 500)");
  });
});

describe("updateCustomer", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("PUTs the name to /api/v1/customers/{id} and returns the updated customer", async () => {
    const updated = { id: 1001, name: "Initrode", timelineSummary: { entryCount: 0, latestOccurredOn: null } };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, updated));
    stubFetch(fetchMock);

    const result = await updateCustomer(1001, { name: "Initrode" });

    expect(result).toEqual(updated);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "Initrode" }),
    });
  });

  it("throws a generic error on 404", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    await expect(updateCustomer(999999, { name: "Ghost" })).rejects.toThrow("Request failed (HTTP 404)");
  });
});

describe("customerQueryOptions", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fetches a single customer by id", async () => {
    const customer = { id: 1001, name: "Acme", timelineSummary: { entryCount: 0, latestOccurredOn: null } };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, customer));
    stubFetch(fetchMock);

    const options = customerQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({
      signal: undefined,
    });

    expect(result).toEqual(customer);
    expect(options.queryKey).toEqual(["customers", 1001]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", { signal: undefined });
  });

  it("throws NotFoundError on 404", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    const options = customerQueryOptions(999999);
    const error = await (options.queryFn as (context: unknown) => Promise<unknown>)({
      signal: undefined,
    }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(NotFoundError);
  });
});

describe("customersQueryOptions", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("builds the query string from page, search, status, type and sort", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        data: [],
        pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: false },
      }),
    );
    stubFetch(fetchMock);

    const options = customersQueryOptions({
      page: 2,
      pageSize: 25,
      search: "923 609 016",
      status: "archived",
      type: "business",
      sortBy: "customerNumber",
      sortDirection: "desc",
    });
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/customers?page=2&pageSize=25&search=923+609+016&status=archived&type=business&sortBy=customerNumber&sortDirection=desc",
      { signal: undefined },
    );
  });

  it("omits status, type and sort from the query string when absent", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(200, {
        data: [],
        pagination: { page: 1, pageSize: 25, totalCount: 0, totalPages: 0, hasNextPage: false, hasPreviousPage: false },
      }),
    );
    stubFetch(fetchMock);

    const options = customersQueryOptions({ page: 1, pageSize: 25 });
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers?page=1&pageSize=25", { signal: undefined });
  });
});

describe("customersListParams", () => {
  it("maps the list page's search state to query params, dropping an empty search string", () => {
    expect(
      customersListParams({
        page: 2,
        search: "",
        status: "active",
        type: "person",
        sortBy: "name",
        sortDirection: "asc",
      }),
    ).toEqual({
      page: 2,
      pageSize: 25,
      search: undefined,
      status: "active",
      type: "person",
      sortBy: "name",
      sortDirection: "asc",
    });
  });

  it("carries no filter or sort when the search state has none", () => {
    expect(customersListParams({ page: 1, search: "acme" })).toEqual({
      page: 1,
      pageSize: 25,
      search: "acme",
      status: undefined,
      type: undefined,
      sortBy: undefined,
      sortDirection: undefined,
    });
  });
});

describe("legalIdentityQueryOptions", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("uses the dedicated legal identity endpoint", async () => {
    const identity = { country: "no", type: "business", id: "1", name: "Acme", source: "brreg" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, identity));
    stubFetch(fetchMock);
    const options = legalIdentityQueryOptions(1001);
    await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/legal-identity", { signal: undefined });
  });

  it("returns null when the customer has no legal identity", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 204 })));

    const options = legalIdentityQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toBeNull();
  });
});
