import { QueryClient } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { type CustomerBillingProfile, customerBillingProfileQueryOptions } from "./billing-profile";
import {
  ApiValidationError,
  type CustomerResponse,
  conflictDuplicates,
  createCustomer,
  customerQueryOptions,
  customersListParams,
  customersQueryOptions,
  invalidateCustomersExcept,
  legalIdentityQueryOptions,
  NotFoundError,
  normalizeCustomer as normalizeCustomerForTest,
  syncCustomerRevision,
  updateContactInfo,
  updateCustomer,
  upsertLegalIdentity,
} from "./customers";
import type { ApiConflictError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const identity = { country: "no", type: "business", id: "923609016", name: "Acme AS", source: "manual" };

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

    expect(result).toEqual({ ...updated, owner: null, tags: [] });
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

    expect(result).toEqual({ ...customer, owner: null, tags: [] });
    expect(options.queryKey).toEqual(["customers", 1001]);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001", { signal: undefined });
  });

  it("fills in the contact-info fields the server leaves out with null", async () => {
    // `email`, `phone` and `website` are `omitempty` on the wire, so a
    // customer that has only a phone number arrives with the other two
    // missing rather than null.
    const customer = {
      id: 1001,
      name: "Acme",
      timelineSummary: { entryCount: 0, latestOccurredOn: null },
      contactInfo: { phone: "+47 934 89 731" },
    };
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, customer)));

    const options = customerQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual({
      ...customer,
      contactInfo: { email: null, phone: "+47 934 89 731", website: null },
      owner: null,
      tags: [],
    });
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

describe("conflictDuplicates", () => {
  const conflict = (problem: Record<string, unknown>) => ({ problem }) as ApiConflictError;

  it("reads well-shaped duplicates out of the conflict's problem body", () => {
    const duplicates = [
      { id: 5, customerNumber: 1005, name: "Acme AS", status: "active" },
      { id: 6, customerNumber: 1006, name: "Acme Holding", status: "archived" },
    ];

    expect(conflictDuplicates(conflict({ duplicates }))).toEqual(duplicates);
  });

  it("drops entries that do not match the expected shape rather than showing a broken row", () => {
    const duplicates = [
      { id: 5, customerNumber: 1005, name: "Acme AS", status: "active" },
      { id: "not-a-number", customerNumber: 1006, name: "Bad Row", status: "active" },
      { customerNumber: 1007, name: "Missing id", status: "active" },
      "not even an object",
    ];

    expect(conflictDuplicates(conflict({ duplicates }))).toEqual([
      { id: 5, customerNumber: 1005, name: "Acme AS", status: "active" },
    ]);
  });

  it("returns an empty list when the problem carries no duplicates at all", () => {
    expect(conflictDuplicates(conflict({ title: "Customer revision conflict" }))).toEqual([]);
    expect(conflictDuplicates(conflict({ duplicates: null }))).toEqual([]);
  });
});

describe("upsertLegalIdentity", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("PUTs the five identity fields to the dedicated sub-resource", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, identity));
    stubFetch(fetchMock);

    await upsertLegalIdentity(1001, identity);

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/legal-identity", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(identity),
    });
  });

  it("can overrule a duplicate-identity conflict, which this endpoint's own body carries (design D6)", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, identity));
    stubFetch(fetchMock);

    await upsertLegalIdentity(1001, { ...identity, allowDuplicateIdentity: true });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1001/legal-identity", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...identity, allowDuplicateIdentity: true }),
    });
  });
});

describe("updateContactInfo", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("normalises the returned customer's contact info the same way the GET does", async () => {
    // The 200 body is a full customer, omitted fields and all, and its
    // revision is read straight off it — so it has to arrive in the same
    // shape the cache already holds.
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(200, {
          id: 1001,
          name: "Acme",
          revision: 4,
          timelineSummary: { entryCount: 0, latestOccurredOn: null },
          contactInfo: { email: "hello@acme.test" },
        }),
      ),
    );

    const result = await updateContactInfo(1001, { email: "hello@acme.test", phone: null, website: null, revision: 3 });

    expect(result.contactInfo).toEqual({ email: "hello@acme.test", phone: null, website: null });
  });
});

const cachedCustomer = (revision: number): CustomerResponse => ({
  id: 1001,
  customerNumber: 5001,
  name: "Acme",
  status: "active",
  type: "business",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  identity: null,
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision,
  owner: null,
  tags: [],
});

const cachedProfile = (revision: number): CustomerBillingProfile => ({
  invoiceEmail: "invoices@acme.test",
  reminderEmail: null,
  paymentTermsDays: null,
  currency: null,
  language: null,
  invoiceDelivery: null,
  reminderDelivery: null,
  peppolId: null,
  gln: null,
  buyerReference: null,
  revision,
  peppolLookup: null,
  warnings: [],
});

describe("syncCustomerRevision", () => {
  const customerKey = customerQueryOptions(1001).queryKey;
  const profileKey = customerBillingProfileQueryOptions(1001).queryKey;

  it("writes the fresh revision to both cache entries that carry it", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(customerKey, cachedCustomer(3));
    queryClient.setQueryData(profileKey, cachedProfile(3));

    syncCustomerRevision(queryClient, 1001, 4);

    expect(queryClient.getQueryData(customerKey)).toMatchObject({ name: "Acme", revision: 4 });
    expect(queryClient.getQueryData(profileKey)).toMatchObject({ invoiceEmail: "invoices@acme.test", revision: 4 });
  });

  it("leaves an entry nothing has cached alone rather than inventing one", () => {
    const queryClient = new QueryClient();

    syncCustomerRevision(queryClient, 1001, 4);

    expect(queryClient.getQueryData(customerKey)).toBeUndefined();
    expect(queryClient.getQueryData(profileKey)).toBeUndefined();
  });

  it("has nothing to do for a write that answers no revision (Archive's 204)", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(customerKey, cachedCustomer(3));

    syncCustomerRevision(queryClient, 1001, undefined);

    expect(queryClient.getQueryData(customerKey)).toMatchObject({ revision: 3 });
  });
});

describe("customers list params and normalisation, owner and tags", () => {
  it("normalises an absent owner to null and absent tags to an empty array", () => {
    // Exactly the body the server sends for an unowned, untagged customer:
    // `owner` is omitted entirely (the contract's own wording) and, for a
    // response recorded before this delivery, so is `tags`. Both mean "none",
    // and the boundary is the one place that is decided.
    const raw = {
      id: 1001,
      customerNumber: 5001,
      name: "Equinor",
      status: "active",
      type: "business" as const,
      createdAt: "2026-06-01T10:00:00Z",
      updatedAt: "2026-07-01T10:00:00Z",
      identity: null,
      timelineSummary: { entryCount: 0, latestOccurredOn: null },
    };
    const normalized = normalizeCustomerForTest(raw);
    expect(normalized.owner).toBeNull();
    expect(normalized.tags).toEqual([]);
  });

  it("keeps an owner and its tags as they arrived", () => {
    const normalized = normalizeCustomerForTest({
      id: 1001,
      customerNumber: 5001,
      name: "Equinor",
      status: "active",
      type: "business",
      createdAt: "2026-06-01T10:00:00Z",
      updatedAt: "2026-07-01T10:00:00Z",
      identity: null,
      timelineSummary: { entryCount: 0, latestOccurredOn: null },
      owner: { userId: "u1", displayName: "Kari Nordmann", active: true },
      tags: [{ id: "t1", name: "VIP" }],
    });
    expect(normalized.owner).toEqual({ userId: "u1", displayName: "Kari Nordmann", active: true });
    expect(normalized.tags).toEqual([{ id: "t1", name: "VIP", color: null }]);
  });

  it("puts the two new filters on the query string and leaves them off when unset", () => {
    expect(customersListParams({ page: 1, search: "", ownerId: "me", tagId: "t1" })).toMatchObject({
      ownerId: "me",
      tagId: "t1",
    });
    const bare = customersListParams({ page: 1, search: "" });
    expect(bare.ownerId).toBeUndefined();
    expect(bare.tagId).toBeUndefined();
  });
});

describe("invalidateCustomersExcept", () => {
  it("invalidates every customers query but the one the caller just made fresh", () => {
    const queryClient = new QueryClient();
    const profileKey = customerBillingProfileQueryOptions(1001).queryKey;
    queryClient.setQueryData(customerQueryOptions(1001).queryKey, cachedCustomer(4));
    queryClient.setQueryData(profileKey, cachedProfile(4));

    invalidateCustomersExcept(queryClient, profileKey);

    expect(queryClient.getQueryState(customerQueryOptions(1001).queryKey)?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(profileKey)?.isInvalidated).toBe(false);
  });
});
