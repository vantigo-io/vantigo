import { describe, expect, it } from "vitest";
import { visiblePlanningNavItems } from "./navigation";
import { validateBoardSearch, validateWindowSearch } from "./search";
import { addWeeks, isIsoDate, isoWeek, mondayOf, weeksBetween } from "./weeks";

describe("weeks", () => {
  it("finds the Monday, the weeks between two days and the ISO week number", () => {
    expect(mondayOf("2026-10-26")).toBe("2026-10-26");
    expect(mondayOf("2026-11-01")).toBe("2026-10-26");
    expect(weeksBetween("2026-10-28", "2026-11-09")).toEqual(["2026-10-26", "2026-11-02", "2026-11-09"]);
    expect(weeksBetween("2026-11-09", "2026-10-28")).toEqual([]);
    expect(addWeeks("2026-12-28", 1)).toBe("2027-01-04");
    expect(isoWeek("2026-10-26")).toBe(44);
    expect(isoWeek("2027-01-01")).toBe(53);
    expect(isoWeek("2021-01-04")).toBe(1);
    expect(isIsoDate("2026-02-30")).toBe(false);
  });
});

describe("the search validators", () => {
  it("keeps what the board understands and drops what a hand-edited link invents", () => {
    expect(
      validateBoardSearch({
        from: "2026-10-26",
        to: "2026-10-01",
        groupBy: "project",
        departmentId: "10",
        competenceId: "x",
        projectId: 100,
        tentative: "false",
        planned: true,
        create: "allocation",
      }),
    ).toEqual({
      from: "2026-10-26",
      groupBy: "project",
      departmentId: 10,
      projectId: 100,
      tentative: false,
      create: "allocation",
    });
    expect(validateBoardSearch({ groupBy: "people", create: "request" })).toEqual({});
    expect(validateWindowSearch({ from: "2026-13-01", to: "2026-11-01" })).toEqual({ to: "2026-11-01" });
  });
});

describe("the sidebar", () => {
  it("offers each destination to the permission the design names", () => {
    expect(visiblePlanningNavItems(["planning:access"]).map((item) => item.key)).toEqual(["myPlanning"]);
    expect(visiblePlanningNavItems(["planning:access", "planning:view"]).map((item) => item.key)).toEqual([
      "board",
      "unbooked",
      "myPlanning",
    ]);
    expect(visiblePlanningNavItems(["*"])).toHaveLength(4);
    expect(visiblePlanningNavItems(undefined)).toEqual([]);
  });
});
