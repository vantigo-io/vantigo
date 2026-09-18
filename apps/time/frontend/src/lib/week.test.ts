import { afterEach, describe, expect, it, vi } from "vitest";
import {
  addDays,
  dayUrl,
  formatHours,
  hoursBetween,
  isIsoDate,
  mondayOf,
  parseHours,
  today,
  weekDays,
  weekUrl,
} from "./week";

describe("mondayOf", () => {
  it("keeps a Monday", () => {
    expect(mondayOf("2026-09-14")).toBe("2026-09-14");
  });

  it("walks a Sunday back to the Monday before it, not forward", () => {
    expect(mondayOf("2026-09-20")).toBe("2026-09-14");
  });

  it("crosses a month and a year boundary", () => {
    expect(mondayOf("2026-10-01")).toBe("2026-09-28");
    expect(mondayOf("2027-01-02")).toBe("2026-12-28");
  });

  it("is not moved by a daylight-saving change in the week", () => {
    // Europe leaves summer time on Sunday 25 October 2026.
    expect(mondayOf("2026-10-25")).toBe("2026-10-19");
    expect(weekDays("2026-10-19").at(-1)).toBe("2026-10-25");
  });
});

describe("weekDays", () => {
  it("lists Monday to Sunday", () => {
    expect(weekDays("2026-09-14")).toEqual([
      "2026-09-14",
      "2026-09-15",
      "2026-09-16",
      "2026-09-17",
      "2026-09-18",
      "2026-09-19",
      "2026-09-20",
    ]);
  });
});

describe("addDays", () => {
  it("moves by weeks in either direction", () => {
    expect(addDays("2026-09-14", -7)).toBe("2026-09-07");
    expect(addDays("2026-09-28", 7)).toBe("2026-10-05");
  });
});

describe("today", () => {
  afterEach(() => vi.useRealTimers());

  it("is the local calendar date, not the UTC one", () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 8, 20, 23, 30));
    expect(today()).toBe("2026-09-20");
  });
});

describe("isIsoDate", () => {
  it("accepts a real calendar date only", () => {
    expect(isIsoDate("2026-09-14")).toBe(true);
    expect(isIsoDate("2026-02-30")).toBe(false);
    expect(isIsoDate("14.09.2026")).toBe(false);
    expect(isIsoDate(undefined)).toBe(false);
  });
});

describe("parseHours", () => {
  it.each([
    ["7.5", 7.5],
    ["7,5", 7.5],
    ["7:30", 7.5],
    [" 8 ", 8],
    ["0:45", 0.75],
    ["7:20", 7.33],
    [",5", 0.5],
    ["24", 24],
    ["1.257", 1.26],
  ])("reads %j as %d hours", (input, hours) => {
    expect(parseHours(input)).toBe(hours);
  });

  it.each(["", "abc", "7.5h", "7:75", "25", "0", "0:00", "-1", "7..5", "7:3", "1,2,3"])("refuses %j", (input) => {
    expect(parseHours(input)).toBeUndefined();
  });
});

describe("formatHours", () => {
  it("writes at most two decimals and no trailing zeros", () => {
    expect(formatHours(7.5)).toBe("7.5");
    expect(formatHours(8)).toBe("8");
    expect(formatHours(7.333)).toBe("7.33");
  });

  it("uses the separator it is given", () => {
    expect(formatHours(7.5, ",")).toBe("7,5");
  });
});

describe("hoursBetween", () => {
  it("is the time between the clock times, to two decimals the way the server derives it", () => {
    expect(hoursBetween("08:00", "15:30")).toBe(7.5);
    expect(hoursBetween("09:00", "09:20")).toBe(0.33);
  });

  it("is undefined when either time is missing or the end is not after the start", () => {
    expect(hoursBetween("08:00", "")).toBeUndefined();
    expect(hoursBetween("", "15:00")).toBeUndefined();
    expect(hoursBetween("15:00", "15:00")).toBeUndefined();
    expect(hoursBetween("15:00", "08:00")).toBeUndefined();
  });
});

describe("urls", () => {
  it("links a week and a day through their search params", () => {
    expect(weekUrl("2026-09-14")).toBe("/time?week=2026-09-14");
    expect(dayUrl("2026-09-16")).toBe("/time/day?date=2026-09-16");
  });
});
