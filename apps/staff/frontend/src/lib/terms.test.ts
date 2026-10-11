import { describe, expect, it } from "vitest";
import {
  appointmentFormsFor,
  defaultFullTimeHours,
  fullTimeHoursMismatch,
  isFtePercent,
  workingHoursSchemes,
} from "./terms";

describe("the terms rules the form previews", () => {
  it("defaults each scheme's hours to a value its own rule accepts", () => {
    for (const scheme of workingHoursSchemes) {
      expect(fullTimeHoursMismatch(scheme, defaultFullTimeHours[scheme]), scheme).toBe(false);
    }
  });

  it("holds each scheme to the a-melding's rule", () => {
    expect(fullTimeHoursMismatch("shift_365", 37.5)).toBe(true);
    expect(fullTimeHoursMismatch("round_the_clock_355", 35)).toBe(true);
    expect(fullTimeHoursMismatch("offshore_336", 28)).toBe(false);
    expect(fullTimeHoursMismatch("continuous_336", 27.9)).toBe(true);
    expect(fullTimeHoursMismatch("continuous_336", 33.7)).toBe(true);
    expect(fullTimeHoursMismatch("not_shift", 40)).toBe(false);
    expect(fullTimeHoursMismatch("other_shift", 169)).toBe(true);
  });

  it("offers only the appointment forms that go with the type", () => {
    expect(appointmentFormsFor("fast")).toEqual(["fast", "fast_utleid"]);
    expect(appointmentFormsFor("midlertidig")).toEqual([
      "midlertidig",
      "midlertidig_utleid",
      "midlertidig_tilkallingsvikar",
    ]);
    expect(appointmentFormsFor(null)).toEqual([]);
  });

  it("takes a percentage above 0, at most 100, with at most two decimals", () => {
    expect(isFtePercent(80.25)).toBe(true);
    expect(isFtePercent(100)).toBe(true);
    expect(isFtePercent(0)).toBe(false);
    expect(isFtePercent(100.5)).toBe(false);
    expect(isFtePercent(33.333)).toBe(false);
  });
});
