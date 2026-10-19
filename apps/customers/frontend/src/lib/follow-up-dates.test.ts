import { setTimeZones } from "@vantigo/frontend-shell";
import { afterEach, describe, expect, it, vi } from "vitest";

import { businessToday, isOverdue } from "./follow-up-dates";

afterEach(() => {
  vi.useRealTimers();
  setTimeZones(null);
});

describe("isOverdue", () => {
  // The boundary a `<=` would get wrong, and the reason this lives in a helper
  // with its own test: due TODAY is still open, not yet late. Only a date
  // computed from the clock can catch that mutation, so the fixtures are
  // relative to `businessToday()` rather than pinned to a year.
  const days = (offset: number) => {
    const [y, m, d] = businessToday().split("-").map(Number);
    return new Date(Date.UTC(y, m - 1, d + offset)).toISOString().slice(0, 10);
  };

  it("calls a past due date overdue", () => {
    expect(isOverdue({ dueOn: days(-1), doneAt: null })).toBe(true);
  });

  it("does not call a follow-up due today overdue", () => {
    expect(isOverdue({ dueOn: businessToday(), doneAt: null })).toBe(false);
  });

  it("does not call a future follow-up overdue", () => {
    expect(isOverdue({ dueOn: days(1), doneAt: null })).toBe(false);
  });

  it("never calls a done follow-up overdue, however old it is", () => {
    // Done is done: the tick is what takes a row off the overdue list, so the
    // date it was due stops mattering the moment `doneAt` is set.
    expect(isOverdue({ dueOn: "2020-01-02", doneAt: "2026-07-22T09:30:00Z" })).toBe(false);
  });

  it("judges today on the business day, not the viewer's own zone", () => {
    // 23:30 UTC on 1 January: already the 2nd in Oslo, still the 1st in New
    // York. A follow-up due the 1st is overdue to the Oslo workspace, whoever
    // reads it.
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T23:30:00Z"));
    setTimeZones({ timeZone: "America/New_York", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    expect(isOverdue({ dueOn: "2026-01-01", doneAt: null })).toBe(true);
  });
});

describe("businessToday", () => {
  it("answers the business zone's calendar day, not the UTC one or the viewer's", () => {
    // 23:30 UTC on 1 January is already 2 January in Oslo.
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T23:30:00Z"));
    setTimeZones({ timeZone: "America/New_York", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    expect(businessToday()).toBe("2026-01-02");
    setTimeZones({ timeZone: "Europe/Oslo", timeZoneSource: "personal", businessTimeZone: "America/New_York" });
    expect(businessToday()).toBe("2026-01-01");
  });
});
