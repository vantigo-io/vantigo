import { customersListParams } from "@vantigo/customers-ui/api/customers";
import { describe, expect, it } from "vitest";
import { Route as CustomersIndexRoute } from "./index";

// Pins that the list route's own URL parser drops anything the API would
// reject rather than forwarding it, and that the loader prefetches on
// exactly the same query key the page's own useQuery builds (both go
// through `customersListParams`) — a mismatch here means the loader's
// prefetch is wasted and the page re-fetches on mount regardless.
describe("the customers list route's search params", () => {
  // toStrictEqual, not toEqual, for every full search object: toEqual treats a
  // key set to undefined as absent, so it could not tell a validator that
  // forgot a filter from one that dropped its value.
  const validate = CustomersIndexRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

  it("falls back to the first page, no search and no filters, dropping what it does not know", () => {
    expect(validate({})).toStrictEqual({
      page: 1,
      search: "",
      status: undefined,
      type: undefined,
      sortBy: undefined,
      sortDirection: undefined,
      ownerId: undefined,
      tagId: undefined,
      groupId: undefined,
    });
    expect(validate({ status: "bogus", type: "bogus", sortBy: "bogus", sortDirection: "bogus" })).toStrictEqual({
      page: 1,
      search: "",
      status: undefined,
      type: undefined,
      sortBy: undefined,
      sortDirection: undefined,
      ownerId: undefined,
      tagId: undefined,
      groupId: undefined,
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
    ).toStrictEqual({
      page: 3,
      search: "acme",
      status: "archived",
      type: "person",
      sortBy: "createdAt",
      sortDirection: "desc",
      ownerId: undefined,
      tagId: undefined,
      groupId: undefined,
    });
  });

  it("keeps the two ownership filters it knows and drops the rest", () => {
    const tagId = "0191d4f8-6f1a-7c3a-9b2e-6d5f4c3b2a10";
    expect(validate({ ownerId: "me" })).toMatchObject({ ownerId: "me", tagId: undefined });
    expect(validate({ ownerId: "none" })).toMatchObject({ ownerId: "none" });
    // A user id is not one of them. The list's Owner filter is a two-entry
    // Select ('Mine' and 'Unassigned'), so a uuid in the URL would reach the
    // API as a filter the Select cannot show — it would sit there blank while
    // the rows were narrowed by something invisible.
    expect(validate({ ownerId: tagId })).toMatchObject({ ownerId: undefined });
    // Neither is a filter the API accepts, so the URL never carries it: 'Me' is
    // case-sensitive there, and a bare word is neither a uuid nor a literal.
    expect(validate({ ownerId: "Me", tagId: "notauuid" })).toMatchObject({ ownerId: undefined, tagId: undefined });
    expect(validate({ tagId })).toMatchObject({ tagId });
  });

  it("keeps a group id and the no-group literal, and drops anything else", () => {
    const groupId = "0191d4f8-6f1a-7c3a-9b2e-6d5f4c3b2a11";
    expect(validate({ groupId })).toMatchObject({ groupId });
    expect(validate({ groupId: "none" })).toMatchObject({ groupId: "none" });
    // 'None' is case-sensitive at the API and a bare word is neither a uuid nor
    // the literal, so the URL never carries either.
    expect(validate({ groupId: "None" })).toMatchObject({ groupId: undefined });
    expect(validate({ groupId: "notauuid" })).toMatchObject({ groupId: undefined });
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
