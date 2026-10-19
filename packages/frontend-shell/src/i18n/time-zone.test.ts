import { afterEach, describe, expect, it, vi } from "vitest";
import { createLocaleFormatters } from "./format";
import {
  browserTimeZone,
  civilDateIn,
  getTimeZones,
  instantFromWallClock,
  setTimeZones,
  subscribeToTimeZones,
  wallClockIn,
} from "./time-zone";

afterEach(() => {
  setTimeZones(null);
});

describe("the time zones store", () => {
  it("starts on the browser's zone and goes back to it without a session", () => {
    expect(getTimeZones()).toEqual({
      timeZone: browserTimeZone(),
      source: "business",
      businessTimeZone: browserTimeZone(),
    });
    setTimeZones({ timeZone: "Asia/Kolkata", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    setTimeZones(null);
    expect(getTimeZones().timeZone).toBe(browserTimeZone());
  });

  it("holds the session's effective and business zones, and tells listeners once per change", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeToTimeZones(listener);
    const session = {
      timeZone: "America/New_York",
      timeZoneSource: "personal",
      businessTimeZone: "Europe/Oslo",
    } as const;
    setTimeZones(session);
    setTimeZones(session);
    expect(getTimeZones()).toEqual({
      timeZone: "America/New_York",
      source: "personal",
      businessTimeZone: "Europe/Oslo",
    });
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it("falls back to the business zone, then the browser's, for a name it cannot format in", () => {
    setTimeZones({ timeZone: "Mars/Olympus_Mons", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    expect(getTimeZones()).toEqual({ timeZone: "Europe/Oslo", source: "business", businessTimeZone: "Europe/Oslo" });
    setTimeZones({ businessTimeZone: "Nowhere/Atall" });
    expect(getTimeZones().businessTimeZone).toBe(browserTimeZone());
  });
});

describe("the shell's formatters", () => {
  const instant = "2026-07-20T22:30:00Z";

  it("show an instant in the zone the session set, whatever the browser's is", () => {
    setTimeZones({ timeZone: "Europe/Oslo", timeZoneSource: "business", businessTimeZone: "Europe/Oslo" });
    expect(createLocaleFormatters("en").formatDate(instant, { timeStyle: "short", hourCycle: "h23" })).toBe("00:30");
    setTimeZones({ timeZone: "America/New_York", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    expect(createLocaleFormatters("en").formatDate(instant, { timeStyle: "short", hourCycle: "h23" })).toBe("18:30");
  });

  it("let a caller's own timeZone win, so a date-only value never shifts", () => {
    setTimeZones({ timeZone: "America/New_York", timeZoneSource: "personal", businessTimeZone: "Europe/Oslo" });
    expect(createLocaleFormatters("en").formatDate("2026-07-21T00:00:00Z", { day: "numeric", timeZone: "UTC" })).toBe(
      "21",
    );
  });
});

describe("civil dates and wall clocks in a zone", () => {
  it("answers the day an instant falls on in the zone, not in UTC", () => {
    // 23:30 UTC on 1 January is already 2 January in Oslo.
    expect(civilDateIn("Europe/Oslo", new Date("2026-01-01T23:30:00Z"))).toBe("2026-01-02");
    expect(civilDateIn("UTC", new Date("2026-01-01T23:30:00Z"))).toBe("2026-01-01");
    expect(civilDateIn("America/New_York", new Date("2026-01-02T03:00:00Z"))).toBe("2026-01-01");
  });

  it("reads a typed date and time as the moment it is in the zone, and back", () => {
    const at = instantFromWallClock("2026-07-20", "09:15", "Europe/Oslo");
    expect(at.toISOString()).toBe("2026-07-20T07:15:00.000Z");
    expect(wallClockIn("Europe/Oslo", at)).toBe("09:15");
    expect(instantFromWallClock("2026-01-20", "09:15", "Europe/Oslo").toISOString()).toBe("2026-01-20T08:15:00.000Z");
    expect(instantFromWallClock("2026-07-20", "23:30", "America/New_York").toISOString()).toBe(
      "2026-07-21T03:30:00.000Z",
    );
  });

  it("takes the first of an autumn hour that happens twice, and moves a spring gap's time on", () => {
    // Oslo's clock goes back from 03:00 to 02:00 on 25 October 2026.
    expect(instantFromWallClock("2026-10-25", "02:30", "Europe/Oslo").toISOString()).toBe("2026-10-25T00:30:00.000Z");
    // And jumps from 02:00 to 03:00 on 29 March 2026: 02:30 is read as 03:30.
    expect(instantFromWallClock("2026-03-29", "02:30", "Europe/Oslo").toISOString()).toBe("2026-03-29T01:30:00.000Z");
  });
});
