import { describe, expect, it } from "vitest";
import type { TaskDependency } from "../api/dependencies";
import {
  datedDownstreamCount,
  dateToUtcDays,
  downstreamTaskIds,
  dueDelta,
  headerSegments,
  isoWeek,
  isTimelineZoom,
  moveTaskDates,
  pixelsToDays,
  utcDaysToDateStr,
} from "./timeline";

describe("timeline dates", () => {
  it("round-trips a calendar date through epoch days", () => {
    expect(utcDaysToDateStr(dateToUtcDays("2026-11-01"))).toBe("2026-11-01");
    expect(dateToUtcDays("1970-01-02")).toBe(1);
  });

  it("snaps a pointer distance to the nearest whole day", () => {
    expect(pixelsToDays(0, 40)).toBe(0);
    expect(pixelsToDays(19, 40)).toBe(0);
    expect(pixelsToDays(21, 40)).toBe(1);
    expect(pixelsToDays(-85, 40)).toBe(-2);
    expect(pixelsToDays(-1, 40)).toBe(0);
    expect(pixelsToDays(16, 5)).toBe(3);
  });

  it("knows its three scales", () => {
    expect(isTimelineZoom("week")).toBe(true);
    expect(isTimelineZoom("year")).toBe(false);
  });
});

describe("moveTaskDates", () => {
  const both = { startDate: "2026-11-02", dueDate: "2026-11-06" };

  it("moves both dates with the body", () => {
    expect(moveTaskDates(both, "move", 3)).toEqual({ startDate: "2026-11-05", dueDate: "2026-11-09" });
    expect(moveTaskDates(both, "move", -2)).toEqual({ startDate: "2026-10-31", dueDate: "2026-11-04" });
  });

  it("moves one date with its edge and never lets the due date fall before the start", () => {
    expect(moveTaskDates(both, "end", 2)).toEqual({ startDate: "2026-11-02", dueDate: "2026-11-08" });
    expect(moveTaskDates(both, "end", -10)).toEqual({ startDate: "2026-11-02", dueDate: "2026-11-02" });
    expect(moveTaskDates(both, "start", -1)).toEqual({ startDate: "2026-11-01", dueDate: "2026-11-06" });
    expect(moveTaskDates(both, "start", 10)).toEqual({ startDate: "2026-11-06", dueDate: "2026-11-06" });
  });

  it("moves the one date a task has, whichever part was held", () => {
    expect(moveTaskDates({ dueDate: "2026-11-06" }, "start", 1)).toEqual({ startDate: null, dueDate: "2026-11-07" });
    expect(moveTaskDates({ startDate: "2026-11-02" }, "end", -1)).toEqual({
      startDate: "2026-11-01",
      dueDate: null,
    });
  });

  it("measures the due date's move", () => {
    expect(dueDelta(both, moveTaskDates(both, "move", 4))).toBe(4);
    expect(dueDelta(both, moveTaskDates(both, "start", 1))).toBe(0);
    expect(dueDelta({ startDate: "2026-11-02" }, { startDate: "2026-11-03" })).toBe(0);
  });
});

describe("downstreamTaskIds", () => {
  const link = (id: number, predecessorTaskId: number, successorTaskId: number): TaskDependency => ({
    id,
    projectId: 7,
    predecessorTaskId,
    successorTaskId,
    createdAt: "2026-10-31T00:00:00Z",
  });

  it("reaches direct and indirect successors once each", () => {
    // 1 → 2 → 4, 1 → 3 → 4: a diamond reaches 4 once.
    const deps = [link(1, 1, 2), link(2, 1, 3), link(3, 2, 4), link(4, 3, 4), link(5, 9, 1)];
    expect([...downstreamTaskIds(1, deps)].sort()).toEqual([2, 3, 4]);
    expect(downstreamTaskIds(4, deps).size).toBe(0);
  });

  it("counts only the reachable tasks with a date to shift, as the server shifts them", () => {
    const deps = [link(1, 1, 2), link(2, 2, 3), link(3, 1, 4)];
    const tasks = [
      { id: 2, dueDate: "2026-11-06" },
      { id: 3, startDate: null, dueDate: null }, // reached, but nothing to move
      { id: 4, startDate: "2026-11-09" },
    ];
    expect(datedDownstreamCount(1, deps, tasks)).toBe(2);
    expect(datedDownstreamCount(1, deps, [])).toBe(0);
  });
});

describe("timeline header", () => {
  it("numbers ISO weeks", () => {
    expect(isoWeek(dateToUtcDays("2026-11-02"))).toBe(45); // a Monday
    expect(isoWeek(dateToUtcDays("2026-11-08"))).toBe(45); // its Sunday
    expect(isoWeek(dateToUtcDays("2027-01-01"))).toBe(53); // a Friday, still 2026's last week
    expect(isoWeek(dateToUtcDays("2026-01-01"))).toBe(1); // a Thursday
  });

  it("cuts the range into days, Monday weeks or calendar months", () => {
    const start = dateToUtcDays("2026-10-29"); // a Thursday
    expect(headerSegments(start, 3, "day").map((s) => s.length)).toEqual([1, 1, 1]);
    const weeks = headerSegments(start, 12, "week");
    expect(weeks.map((s) => [utcDaysToDateStr(s.firstDay), s.offset, s.length])).toEqual([
      ["2026-10-29", 0, 4],
      ["2026-11-02", 4, 7],
      ["2026-11-09", 11, 1],
    ]);
    const months = headerSegments(start, 40, "month");
    expect(months.map((s) => [s.key, s.length])).toEqual([
      ["2026-10", 3],
      ["2026-11", 30],
      ["2026-12", 7],
    ]);
  });
});
