import { describe, expect, it } from "vitest";

import { isOverdue, utcToday } from "./follow-up-dates";

describe("isOverdue", () => {
  // The boundary a `<=` would get wrong, and the reason this lives in a helper
  // with its own test: due TODAY is still open, not yet late. Only a date
  // computed from the clock can catch that mutation, so the fixtures are
  // relative to `utcToday()` rather than pinned to a year.
  const days = (offset: number) => new Date(Date.now() + offset * 86_400_000).toISOString().slice(0, 10);

  it("calls a past due date overdue", () => {
    expect(isOverdue({ dueOn: days(-1), doneAt: null })).toBe(true);
  });

  it("does not call a follow-up due today overdue", () => {
    expect(isOverdue({ dueOn: utcToday(), doneAt: null })).toBe(false);
  });

  it("does not call a future follow-up overdue", () => {
    expect(isOverdue({ dueOn: days(1), doneAt: null })).toBe(false);
  });

  it("never calls a done follow-up overdue, however old it is", () => {
    // Done is done: the tick is what takes a row off the overdue list, so the
    // date it was due stops mattering the moment `doneAt` is set.
    expect(isOverdue({ dueOn: "2020-01-02", doneAt: "2026-07-22T09:30:00Z" })).toBe(false);
  });
});

describe("utcToday", () => {
  it("answers a date-only value on the UTC calendar", () => {
    expect(utcToday()).toBe(new Date().toISOString().slice(0, 10));
    expect(utcToday()).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });
});
