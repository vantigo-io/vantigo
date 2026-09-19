import { describe, expect, it } from "vitest";
import { budgetBarGeometry, budgetUnit, isEconomySort, segmentValue, validateEconomyPortfolioSearch } from "./economy";

const buckets = (approved: number, submitted: number, draft: number) => ({
  approved: { hours: approved },
  submitted: { hours: submitted },
  draft: { hours: draft },
});

describe("budgetUnit", () => {
  it("measures a budget amount and a fixed price in money, and everything else in hours", () => {
    expect(budgetUnit("amount")).toBe("amount");
    expect(budgetUnit("fixedPrice")).toBe("amount");
    expect(budgetUnit("hours")).toBe("hours");
    expect(budgetUnit(undefined)).toBe("hours");
  });
});

describe("segmentValue", () => {
  // A bucket whose amount the API left out is worth nothing in money — but it
  // is still a bucket, and its hours are still its hours.
  it("counts a bucket with no amount as nothing on the money unit, and its hours on the hours unit", () => {
    expect(segmentValue({ hours: 12 }, "amount")).toBe(0);
    expect(segmentValue({ hours: 12 }, "hours")).toBe(12);
    expect(segmentValue({ hours: 12, amount: 9600 }, "amount")).toBe(9600);
  });
});

describe("budgetBarGeometry", () => {
  it("measures the three buckets against the budget while they fit inside it", () => {
    const geometry = budgetBarGeometry(buckets(210, 62, 40), "hours", 400);

    expect(geometry.total).toBe(312);
    expect(geometry.scale).toBe(400);
    expect(geometry.widths).toEqual({ approved: 52.5, submitted: 15.5, draft: 10 });
    expect(geometry.marker).toBe(100);
    expect(geometry.overflow).toBeUndefined();
  });

  // The marker has to stay inside the bar, so an over-budget bar is scaled by
  // what was logged rather than by what was budgeted.
  it("measures against the total once the work has passed the budget, so the marker stays in the bar", () => {
    const geometry = budgetBarGeometry(buckets(300, 150, 50), "hours", 400);

    expect(geometry.scale).toBe(500);
    expect(geometry.widths).toEqual({ approved: 60, submitted: 30, draft: 10 });
    expect(geometry.marker).toBe(80);
    expect(geometry.overflow).toEqual({ from: 80, to: 100 });
  });

  it("counts work that lands exactly on the budget as no overflow at all", () => {
    const geometry = budgetBarGeometry(buckets(200, 150, 50), "hours", 400);

    expect(geometry.marker).toBe(100);
    expect(geometry.overflow).toBeUndefined();
  });

  it("draws the buckets against their own total when there is no budget to mark", () => {
    const geometry = budgetBarGeometry(buckets(60, 30, 10), "hours", undefined);

    expect(geometry.scale).toBe(100);
    expect(geometry.widths).toEqual({ approved: 60, submitted: 30, draft: 10 });
    expect(geometry.marker).toBeUndefined();
    expect(geometry.overflow).toBeUndefined();
  });

  it("gives a bucket with nothing in it no width at all", () => {
    const geometry = budgetBarGeometry(buckets(100, 0, 0), "hours", 200);

    expect(geometry.widths).toEqual({ approved: 50, submitted: 0, draft: 0 });
  });

  // Nothing logged and nothing budgeted must not divide by zero: the bar is
  // simply empty.
  it("has nothing to draw when nothing is logged and nothing is budgeted", () => {
    const geometry = budgetBarGeometry(buckets(0, 0, 0), "hours", undefined);

    expect(geometry.scale).toBe(0);
    expect(geometry.total).toBe(0);
    expect(geometry.widths).toEqual({ approved: 0, submitted: 0, draft: 0 });
    expect(geometry.marker).toBeUndefined();
  });

  it("measures money buckets in money", () => {
    const geometry = budgetBarGeometry(
      {
        approved: { hours: 210, amount: 240000 },
        submitted: { hours: 62, amount: 120000 },
        draft: { hours: 40, amount: 0 },
      },
      "amount",
      480000,
    );

    expect(geometry.total).toBe(360000);
    expect(geometry.widths).toEqual({ approved: 50, submitted: 25, draft: 0 });
  });
});

describe("isEconomySort", () => {
  it("knows the four orders the portfolio offers", () => {
    expect(isEconomySort("budgetUsed")).toBe(true);
    expect(isEconomySort("readyAmount")).toBe(true);
    expect(isEconomySort("nextMilestone")).toBe(true);
    expect(isEconomySort("code")).toBe(true);
    expect(isEconomySort("margin")).toBe(false);
  });
});

describe("validateEconomyPortfolioSearch", () => {
  it("lands on the active projects, most-used first, on the first page", () => {
    expect(validateEconomyPortfolioSearch({})).toEqual({
      page: 1,
      search: "",
      status: "active",
      customerId: undefined,
      overBudget: false,
      hasReady: false,
      sort: "budgetUsed",
    });
  });

  it("keeps every status the API knows, 'all' included", () => {
    expect(validateEconomyPortfolioSearch({ status: "on-hold" }).status).toBe("on-hold");
    expect(validateEconomyPortfolioSearch({ status: "all" }).status).toBe("all");
  });

  it("reads the filters a shared link carries", () => {
    expect(
      validateEconomyPortfolioSearch({
        page: "3",
        search: "web",
        status: "all",
        customerId: "1001",
        overBudget: "true",
        hasReady: true,
        sort: "readyAmount",
      }),
    ).toEqual({
      page: 3,
      search: "web",
      status: "all",
      customerId: 1001,
      overBudget: true,
      hasReady: true,
      sort: "readyAmount",
    });
  });

  // A hand-edited URL must never reach the API as a value it would refuse, nor
  // as customerId=NaN.
  it("falls back to the defaults for anything the API would refuse", () => {
    expect(
      validateEconomyPortfolioSearch({ page: "0", status: "archived", sort: "margin", customerId: "abc" }),
    ).toEqual({
      page: 1,
      search: "",
      status: "active",
      customerId: undefined,
      overBudget: false,
      hasReady: false,
      sort: "budgetUsed",
    });
  });
});
