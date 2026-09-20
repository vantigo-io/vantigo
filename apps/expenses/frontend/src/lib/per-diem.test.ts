import { describe, expect, it } from "vitest";
import { mealDeductions } from "./per-diem";

/**
 * The counting itself is the server's, and nothing mirrors it here any more:
 * every figure and every day on the page comes from the suggestion endpoint,
 * because the rates are dated and an administrator may change them. What is
 * left in this module is the reading of a line the server has already priced.
 */
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
