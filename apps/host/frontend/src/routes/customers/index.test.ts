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
    });
    expect(validate({ status: "bogus", type: "bogus", sortBy: "bogus", sortDirection: "bogus" })).toEqual({
      page: 1,
      search: "",
      status: undefined,
      type: undefined,
      sortBy: undefined,
      sortDirection: undefined,
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
    });
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
