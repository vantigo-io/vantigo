import { describe, expect, it } from "vitest";
import { Route as ExpensesApprovalsRoute } from "./expenses/approvals";
import { Route as ExpensesIndexRoute } from "./expenses/index";
import { Route as ExpensesReimbursementsRoute } from "./expenses/reimbursements";

// The three validators are the package's own (`@vantigo/expenses-ui/lib/search`);
// these pin that the route files hand the URL to them rather than reinventing
// a parser, the way `route-guards.test.ts` pins the projects list's.
describe("the expenses routes' search params", () => {
  it("My expenses falls back to the first page and no filters, and drops what it does not know", () => {
    const validate = ExpensesIndexRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

    // Two kinds of unit come from two paged endpoints, so the travel claims
    // carry a page of their own beside the expenses'.
    expect(validate({})).toEqual({
      status: undefined,
      kind: undefined,
      reimbursed: undefined,
      from: undefined,
      to: undefined,
      page: 1,
      claimPage: 1,
      create: undefined,
    });
    expect(validate({ status: "rejected", page: "3" })).toEqual({
      status: "rejected",
      kind: undefined,
      reimbursed: undefined,
      from: undefined,
      to: undefined,
      page: 3,
      claimPage: 1,
      create: undefined,
    });
    expect(validate({ status: "bogus" })).toMatchObject({ status: undefined });
  });

  // Spotlight's "New travel claim" lands on My expenses with the trip form
  // already open, the way every other app's create action does. Anything else
  // in that slot is no form at all rather than a value the page would not know.
  it("My expenses opens the trip form only for the one create value it knows", () => {
    const validate = ExpensesIndexRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

    expect(validate({ create: "claim" })).toMatchObject({ create: "claim" });
    expect(validate({ create: "expense" })).toMatchObject({ create: undefined });
    expect(validate({ create: true })).toMatchObject({ create: undefined });
  });

  it("Approvals defaults to the waiting queue and the first page, dropping an unknown state", () => {
    const validate = ExpensesApprovalsRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

    // The approved half lists two kinds of unit from two paged endpoints, so
    // the travel claims carry a page of their own beside the expenses'.
    expect(validate({})).toEqual({ state: "waiting", page: 1, claimPage: 1 });
    expect(validate({ state: "approved" })).toEqual({ state: "approved", page: 1, claimPage: 1 });
    expect(validate({ state: "bogus", claimPage: "2" })).toEqual({ state: "waiting", page: 1, claimPage: 2 });
  });

  it("Reimbursements defaults to the waiting list and the first page", () => {
    const validate = ExpensesReimbursementsRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

    expect(validate({})).toEqual({ state: "waiting", from: undefined, to: undefined, page: 1 });
    expect(validate({ state: "reimbursed" })).toEqual({ state: "reimbursed", from: undefined, to: undefined, page: 1 });
  });
});
