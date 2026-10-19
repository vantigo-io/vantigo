import { describe, expect, it } from "vitest";
import { defaultRecurrence, formatRule, parseRule } from "./recurrence";

// A Tuesday, the 3rd: the first Tuesday of November 2026.
const START = "2026-11-03";

describe("the recurrence model", () => {
  it.each([
    ["daily", "FREQ=DAILY;INTERVAL=1"],
    ["every third day, ten times", "FREQ=DAILY;INTERVAL=3;COUNT=10"],
    ["weekly on chosen days", "FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE,FR"],
    ["every other week until a date", "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU;UNTIL=20261231"],
    ["monthly on the day", "FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=21"],
    ["monthly on an ordinal weekday, five times", "FREQ=MONTHLY;INTERVAL=1;BYDAY=2TU;COUNT=5"],
    ["monthly on the last weekday", "FREQ=MONTHLY;INTERVAL=3;BYDAY=-1FR"],
  ])("round-trips %s", (_shape, rule) => {
    expect(formatRule(parseRule(rule, START))).toBe(rule);
  });

  it("reads the parts in any order, a dated-time UNTIL as its day, and writes the canonical order", () => {
    const model = parseRule("BYDAY=WE,MO;UNTIL=20270115T225959Z;FREQ=WEEKLY", START);
    expect(model).toMatchObject({
      freq: "weekly",
      interval: 1,
      weekdays: ["MO", "WE"],
      end: "until",
      until: "2027-01-15",
    });
    expect(formatRule(model)).toBe("FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;UNTIL=20270115");
  });

  it("reads no rule as not repeating, and writes nothing for it", () => {
    expect(parseRule(undefined, START).freq).toBe("none");
    expect(parseRule("", START).freq).toBe("none");
    expect(formatRule(parseRule(undefined, START))).toBeUndefined();
  });

  it("starts a new series from its first day: that weekday, that day of the month, that ordinal", () => {
    expect(defaultRecurrence(START)).toMatchObject({
      weekdays: ["TU"],
      monthDay: 3,
      ordinal: 1,
      ordinalWeekday: "TU",
    });
    expect(defaultRecurrence("2026-10-30")).toMatchObject({ ordinal: -1, ordinalWeekday: "FR", monthDay: 30 });
  });

  it("writes what the control chose when switched from one frequency to another", () => {
    const model = { ...defaultRecurrence(START), freq: "monthly" as const, monthlyMode: "weekday" as const };
    expect(formatRule(model)).toBe("FREQ=MONTHLY;INTERVAL=1;BYDAY=1TU");
    expect(formatRule({ ...model, freq: "weekly" })).toBe("FREQ=WEEKLY;INTERVAL=1;BYDAY=TU");
  });
});
