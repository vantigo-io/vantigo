import { describe, expect, it } from "vitest";
import { calendarWindows, monthOf, workingDays } from "./workdays";

describe("working days", () => {
  it("counts Monday to Friday, both ends included", () => {
    // Monday 12 October to Friday 23 October 2026.
    expect(workingDays("2026-10-12", "2026-10-23", [])).toBe(10);
    // Saturday and Sunday alone.
    expect(workingDays("2026-10-17", "2026-10-18", [])).toBe(0);
    expect(workingDays("2026-10-14", "2026-10-14", [])).toBe(1);
  });

  it("skips the public holidays the calendar answered, and only those", () => {
    expect(workingDays("2026-10-12", "2026-10-16", ["2026-10-14"])).toBe(4);
    // A holiday on a Saturday changes nothing; one outside the range neither.
    expect(workingDays("2026-10-12", "2026-10-16", ["2026-10-17", "2026-11-02"])).toBe(5);
  });

  it("counts nothing for a range that ends before it starts", () => {
    expect(workingDays("2026-10-16", "2026-10-12", [])).toBe(0);
  });
});

describe("the calendar's windows", () => {
  it("asks for a short range in one read", () => {
    expect(calendarWindows("2026-10-12", "2026-10-16")).toEqual([{ from: "2026-10-12", to: "2026-10-16" }]);
  });

  it("splits a long range into reads of at most 93 days that meet without a gap", () => {
    const windows = calendarWindows("2026-01-01", "2026-12-31");
    expect(windows[0]).toEqual({ from: "2026-01-01", to: "2026-04-03" });
    expect(windows.at(-1)?.to).toBe("2026-12-31");
    for (const [index, window] of windows.entries()) {
      const days = (Date.parse(window.to) - Date.parse(window.from)) / 86_400_000 + 1;
      expect(days).toBeLessThanOrEqual(93);
      if (index > 0) {
        const previous = new Date(`${windows[index - 1].to}T00:00:00Z`);
        previous.setUTCDate(previous.getUTCDate() + 1);
        expect(window.from).toBe(previous.toISOString().slice(0, 10));
      }
    }
  });
});

describe("a month", () => {
  it("runs from the first to the last day of today's month", () => {
    expect(monthOf("2026-10-11")).toEqual({ from: "2026-10-01", to: "2026-10-31" });
    expect(monthOf("2028-02-15")).toEqual({ from: "2028-02-01", to: "2028-02-29" });
  });
});
