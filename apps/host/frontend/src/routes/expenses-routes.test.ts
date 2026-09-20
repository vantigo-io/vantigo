import { isNotFound } from "@tanstack/react-router";
import { describe, expect, it } from "vitest";
import { Route as ExpensesApprovalsRoute } from "./expenses/approvals";
import { Route as ExpensesClaimRoute } from "./expenses/claims.$claimId";
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

  // The claim page takes no search params at all; what it does own is the path
  // parameter, and an id that is not a positive whole number must never reach
  // the API as `GET /claims/NaN`.
  it("the travel claim route parses its id and refuses anything that is not one", () => {
    const params = ExpensesClaimRoute.options.params as {
      parse: (raw: { claimId: string }) => { claimId: number };
      stringify: (parsed: { claimId: number }) => { claimId: string };
    };
    expect(params.parse({ claimId: "1012" })).toEqual({ claimId: 1012 });
    expect(params.stringify({ claimId: 1012 })).toEqual({ claimId: "1012" });
    // Decimal digits and nothing else: `Number` alone reads each of these as a
    // number, and the first three would resolve to a real claim under a URL
    // nobody could have linked to.
    for (const raw of ["1e3", "0x10", " 12", "12abc", "", "1.5"]) {
      expect(params.parse({ claimId: raw }).claimId).toBeNaN();
    }

    const guard = ExpensesClaimRoute.options.beforeLoad as (context: { params: { claimId: number } }) => void;
    expect(() => guard({ params: { claimId: 1012 } })).not.toThrow();
    // 1e20 parses as digits but is past what a float can hold exactly, so it
    // would reach the server as an id it cannot answer.
    for (const claimId of [Number("abc"), 0, -3, 1.5, 1e20]) {
      let thrown: unknown;
      try {
        guard({ params: { claimId } });
      } catch (error) {
        thrown = error;
      }
      expect(isNotFound(thrown)).toBe(true);
    }
  });
});
