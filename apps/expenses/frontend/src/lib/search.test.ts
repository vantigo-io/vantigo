import { describe, expect, it } from "vitest";
import { validateMyExpensesSearch } from "./search";

describe("validateMyExpensesSearch", () => {
  it("is no filter at all when the URL carries nothing", () => {
    expect(validateMyExpensesSearch({})).toEqual({
      status: undefined,
      kind: undefined,
      reimbursed: undefined,
      from: undefined,
      to: undefined,
      page: 1,
    });
  });

  it("keeps the values the API knows", () => {
    expect(
      validateMyExpensesSearch({
        status: "submitted",
        kind: "mileage",
        reimbursed: "false",
        from: "2026-01-01",
        to: "2026-03-31",
        page: "3",
      }),
    ).toEqual({
      status: "submitted",
      kind: "mileage",
      reimbursed: false,
      from: "2026-01-01",
      to: "2026-03-31",
      page: 3,
    });
  });

  it("drops what a hand-edited link invented rather than sending the API a 400", () => {
    const search = validateMyExpensesSearch({
      status: "paid",
      kind: "per_diem",
      reimbursed: "maybe",
      from: "2026-02-30",
      to: "yesterday",
      page: "-4",
    });
    expect(search).toEqual({
      status: undefined,
      kind: undefined,
      reimbursed: undefined,
      from: undefined,
      to: undefined,
      page: 1,
    });
  });
});
