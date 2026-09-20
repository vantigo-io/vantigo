import { describe, expect, it } from "vitest";
import { mealDeductions, suggestedDayCount } from "./per-diem";

const trip = (hours: number, minutes = 0) => ({
  departureAt: "2026-03-09T07:00:00Z",
  returnAt: new Date(Date.UTC(2026, 2, 9, 7 + hours, minutes)).toISOString(),
});

/**
 * The counting is the server's, mirrored here for one sentence of explanation
 * beside the button. Every figure on the page still comes from the suggestion
 * endpoint — the rates are dated and an administrator may change them — so
 * this is a count of days and never an amount.
 */
describe("suggestedDayCount", () => {
  it("counts nothing for a trip under six hours", () => {
    const { departureAt, returnAt } = trip(5, 59);
    expect(suggestedDayCount(departureAt, returnAt, false)).toBe(0);
    expect(suggestedDayCount(departureAt, returnAt, true)).toBe(0);
  });

  it("counts one day for a trip that stays inside a day", () => {
    expect(suggestedDayCount(trip(6).departureAt, trip(6).returnAt, false)).toBe(1);
    expect(suggestedDayCount(trip(30).departureAt, trip(30).returnAt, false)).toBe(1);
  });

  it("counts a full period, and a remainder only when it is longer than six hours", () => {
    const count = (hours: number, minutes = 0) => {
      const { departureAt, returnAt } = trip(hours, minutes);
      return suggestedDayCount(departureAt, returnAt, true);
    };
    expect(count(6)).toBe(1);
    expect(count(24)).toBe(1);
    expect(count(30)).toBe(1);
    expect(count(30, 1)).toBe(2);
    expect(count(48)).toBe(2);
    expect(count(54, 1)).toBe(3);
  });

  it("counts nothing when the return is before the departure", () => {
    expect(suggestedDayCount("2026-03-11T07:00:00Z", "2026-03-09T07:00:00Z", true)).toBe(0);
  });
});

describe("mealDeductions", () => {
  it("lists a covered meal with the percentage the day was priced at", () => {
    expect(
      mealDeductions({
        type: "day_over_12",
        breakfastCovered: true,
        lunchCovered: false,
        dinnerCovered: true,
        dayRate: 736,
        mealPercents: { breakfast: 20, lunch: 30, dinner: 50 },
      }),
    ).toEqual([
      { meal: "breakfast", covered: true, percent: 20 },
      { meal: "lunch", covered: false, percent: 30 },
      { meal: "dinner", covered: true, percent: 50 },
    ]);
  });

  it("leaves a percentage the table never priced absent rather than calling it zero", () => {
    const deductions = mealDeductions({
      type: "day_6_12",
      breakfastCovered: false,
      lunchCovered: false,
      dinnerCovered: false,
      dayRate: 397,
      mealPercents: { breakfast: 20 },
    });
    expect(deductions.map((one) => one.percent)).toEqual([20, undefined, undefined]);
  });
});
