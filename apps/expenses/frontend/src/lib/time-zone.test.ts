import { describe, expect, it } from "vitest";
import { instantInZone, wallClockInZone, zoneCalendarDate, zoneOffsetMinutes } from "./time-zone";

/**
 * The installation's business time zone is the one a trip's days are taken in
 * — never the browser's. Every expectation below is an absolute one about the
 * named zone, so it holds whatever zone the test process itself runs in: on
 * this repo's machines that is UTC, which is precisely the disagreement the
 * page has to survive, and in Oslo the same assertions still describe Oslo.
 */
describe("instantInZone", () => {
  it("sends a winter wall-clock time with the zone's winter offset", () => {
    expect(instantInZone("2026-03-09", "07:00", "Europe/Oslo")).toBe("2026-03-09T07:00:00+01:00");
  });

  it("sends a summer wall-clock time with the zone's summer offset", () => {
    expect(instantInZone("2026-07-01", "00:30", "Europe/Oslo")).toBe("2026-07-01T00:30:00+02:00");
  });

  it("is the same wall clock in a zone west of Greenwich", () => {
    expect(instantInZone("2026-02-09", "07:00", "America/New_York")).toBe("2026-02-09T07:00:00-05:00");
  });

  it("writes UTC as a zero offset rather than a bare Z", () => {
    expect(instantInZone("2026-03-09", "07:00", "UTC")).toBe("2026-03-09T07:00:00+00:00");
  });

  it("falls back to the browser's own offset when the zone is unknown to it", () => {
    // Never a guess dressed up as the installation's zone: an unparseable name
    // still produces a valid instant, so a save is refused by the server for a
    // reason it can explain rather than by a crash in the form.
    expect(instantInZone("2026-03-09", "07:00", "Nowhere/Atlantis")).toMatch(/^2026-03-09T07:00:00[+-]\d{2}:\d{2}$/);
  });
});

describe("zoneOffsetMinutes", () => {
  it("knows the day a zone moves its clocks", () => {
    // 2026-03-29 02:00 Oslo is when the spring forward happens.
    expect(zoneOffsetMinutes("Europe/Oslo", new Date("2026-03-29T00:59:00Z"))).toBe(60);
    expect(zoneOffsetMinutes("Europe/Oslo", new Date("2026-03-29T01:01:00Z"))).toBe(120);
  });
});

describe("wallClockInZone", () => {
  it("reads a stored instant back as the clock the traveller looked at", () => {
    expect(wallClockInZone("2026-03-09T06:00:00Z", "Europe/Oslo")).toEqual({ date: "2026-03-09", time: "07:00" });
  });

  it("is the previous day in the zone when the instant is just past midnight in UTC", () => {
    expect(wallClockInZone("2026-06-30T22:30:00Z", "Europe/Oslo")).toEqual({ date: "2026-07-01", time: "00:30" });
  });
});

describe("zoneCalendarDate", () => {
  it("is the day the trip departed in the installation's zone, not the browser's", () => {
    expect(zoneCalendarDate("2026-06-30T22:30:00Z", "Europe/Oslo")).toBe("2026-07-01");
    expect(zoneCalendarDate("2026-06-30T22:30:00Z", "UTC")).toBe("2026-06-30");
  });
});
