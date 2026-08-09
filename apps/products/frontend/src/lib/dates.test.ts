import { describe, expect, it } from "vitest";
import { toIsoTimestamp, toPickerValue } from "./dates";

describe("toIsoTimestamp", () => {
  it("converts a local datetime picker string to a UTC ISO timestamp", () => {
    const local = new Date("2026-08-10T14:30:00");
    expect(toIsoTimestamp("2026-08-10 14:30:00")).toBe(local.toISOString());
  });

  it("interprets a date-only value as local midnight", () => {
    const local = new Date("2026-08-10T00:00:00");
    expect(toIsoTimestamp("2026-08-10")).toBe(local.toISOString());
  });

  it("returns undefined for null so optional validity windows stay unset", () => {
    expect(toIsoTimestamp(null)).toBeUndefined();
  });

  it("does not throw on string input (regression: pickers return strings, not Dates)", () => {
    // The previous implementation called value.toISOString() and crashed when
    // Mantine handed the form a plain string.
    expect(() => toIsoTimestamp("2027-01-01")).not.toThrow();
  });
});

describe("toPickerValue", () => {
  it("round-trips with toIsoTimestamp", () => {
    const iso = toIsoTimestamp("2026-08-10 14:30:00");
    expect(toPickerValue(iso ?? null)).toBe("2026-08-10 14:30:00");
  });

  it("returns null for null", () => {
    expect(toPickerValue(null)).toBeNull();
  });
});
