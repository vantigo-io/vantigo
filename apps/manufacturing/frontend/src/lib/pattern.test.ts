import { describe, expect, it } from "vitest";
import type { ManufacturingWeekPattern } from "../api/work-centers";
import { defaultPattern, endTimes, minutesOf, nextInterval, openMinutes, patternIssues, startTimes } from "./pattern";

const withDay = (day: keyof ManufacturingWeekPattern, intervals: ManufacturingWeekPattern["monday"]) => ({
  ...defaultPattern(),
  [day]: intervals,
});

describe("the weekly pattern", () => {
  it("defaults to Monday to Friday 07:00–15:00 and the weekend closed, which is valid", () => {
    const pattern = defaultPattern();
    expect(pattern.monday).toEqual([{ start: "07:00", end: "15:00" }]);
    expect(pattern.saturday).toEqual([]);
    expect(pattern.sunday).toEqual([]);
    expect(patternIssues(pattern)).toEqual([]);
    expect(openMinutes(pattern.friday)).toBe(480);
  });

  it("offers the 15-minute grid, 24:00 only as an end", () => {
    expect(startTimes[0]).toBe("00:00");
    expect(startTimes.at(-1)).toBe("23:45");
    expect(startTimes).not.toContain("24:00");
    expect(endTimes[0]).toBe("00:15");
    expect(endTimes.at(-1)).toBe("24:00");
    expect(startTimes).toHaveLength(96);
    expect(minutesOf("24:00")).toBe(1440);
    expect(minutesOf("24:15")).toBeNaN();
    expect(minutesOf("7:00")).toBeNaN();
  });

  it("accepts two non-overlapping intervals, including one that ends at 24:00", () => {
    const pattern = withDay("monday", [
      { start: "06:00", end: "14:00" },
      { start: "14:00", end: "24:00" },
    ]);
    expect(patternIssues(pattern)).toEqual([]);
  });

  it("refuses a start at or after its end", () => {
    expect(patternIssues(withDay("tuesday", [{ start: "15:00", end: "07:00" }]))).toEqual([
      { day: "tuesday", index: 0, problem: "order" },
    ]);
    expect(patternIssues(withDay("tuesday", [{ start: "08:00", end: "08:00" }]))).toEqual([
      { day: "tuesday", index: 0, problem: "order" },
    ]);
  });

  it("refuses a time off the 15-minute grid, and 24:00 as a start", () => {
    expect(patternIssues(withDay("monday", [{ start: "07:10", end: "15:00" }]))).toEqual([
      { day: "monday", index: 0, problem: "grid" },
    ]);
    expect(patternIssues(withDay("monday", [{ start: "24:00", end: "24:00" }]))).toEqual([
      { day: "monday", index: 0, problem: "grid" },
    ]);
  });

  it("refuses two intervals that overlap, and a third interval", () => {
    expect(
      patternIssues(
        withDay("wednesday", [
          { start: "07:00", end: "12:00" },
          { start: "11:00", end: "15:00" },
        ]),
      ),
    ).toEqual([{ day: "wednesday", index: 1, problem: "overlap" }]);
    expect(
      patternIssues(
        withDay("sunday", [
          { start: "06:00", end: "08:00" },
          { start: "09:00", end: "10:00" },
          { start: "11:00", end: "12:00" },
        ]),
      ),
    ).toEqual([{ day: "sunday", problem: "tooMany" }]);
  });

  it("adds a second interval after the first, within the day", () => {
    expect(nextInterval([])).toEqual({ start: "07:00", end: "15:00" });
    expect(nextInterval([{ start: "07:00", end: "15:00" }])).toEqual({ start: "15:00", end: "19:00" });
    expect(nextInterval([{ start: "14:00", end: "22:00" }])).toEqual({ start: "22:00", end: "24:00" });
  });
});
