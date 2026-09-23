import { customersListParams } from "@vantigo/customers-ui/api/customers";
import { describe, expect, it } from "vitest";
import { Route as CustomersIndexRoute } from "./index";

// Pins that the list route's own URL parser drops anything the API would
// reject rather than forwarding it, and that the loader prefetches on
// exactly the same query key the page's own useQuery builds (both go
// through `customersListParams`) — a mismatch here means the loader's
// prefetch is wasted and the page re-fetches on mount regardless.
describe("the customers list route's search params", () => {
  const validate = CustomersIndexRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

  it("falls back to the first page, no search and no filters, dropping what it does not know", () => {
    expect(validate({})).toEqual({
      page: 1,
      search: "",
      status: undefined,
      type: undefined,
      sortBy: undefined,
      sortDirection: undefined,
      ownerId: undefined,
      tagId: undefined,
    });
    expect(validate({ status: "bogus", type: "bogus", sortBy: "bogus", sortDirection: "bogus" })).toEqual({
      page: 1,
      search: "",
      status: undefined,
      type: undefined,
      sortBy: undefined,
      sortDirection: undefined,
      ownerId: undefined,
      tagId: undefined,
    });
  });

  it("keeps a valid status, type and sort", () => {
    expect(
      validate({
        page: "3",
        search: "acme",
        status: "archived",
        type: "person",
        sortBy: "createdAt",
        sortDirection: "desc",
      }),
    ).toEqual({
      page: 3,
      search: "acme",
      status: "archived",
      type: "person",
      sortBy: "createdAt",
      sortDirection: "desc",
      ownerId: undefined,
      tagId: undefined,
    });
  });

  it("keeps the two ownership filters it knows and drops the rest", () => {
    const tagId = "0191d4f8-6f1a-7c3a-9b2e-6d5f4c3b2a10";
    expect(validate({ ownerId: "me" })).toMatchObject({ ownerId: "me", tagId: undefined });
    expect(validate({ ownerId: "none" })).toMatchObject({ ownerId: "none" });
    expect(validate({ ownerId: tagId })).toMatchObject({ ownerId: tagId });
    // Neither is a filter the API accepts, so the URL never carries it: 'Me' is
    // case-sensitive there, and a bare word is neither a uuid nor a literal.
    expect(validate({ ownerId: "Me", tagId: "notauuid" })).toMatchObject({ ownerId: undefined, tagId: undefined });
    expect(validate({ tagId })).toMatchObject({ tagId });
  });

  it("opens the create form only for the one create value it knows", () => {
    expect(validate({ create: "true" })).toMatchObject({ create: true });
    expect(validate({ create: true })).toMatchObject({ create: true });
    expect(validate({})).not.toHaveProperty("create");
  });

  it("hands the loader exactly the params the page itself would build from the same search", () => {
    const loaderDeps = CustomersIndexRoute.options.loaderDeps as (context: { search: unknown }) => unknown;
    const search = {
      page: 2,
      search: "acme",
      status: "active",
      type: "business",
      sortBy: "name",
      sortDirection: "asc",
    } as const;

    expect(loaderDeps({ search })).toEqual(customersListParams(search));
  });
});
