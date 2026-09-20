import type { components } from "../api-schema";

type Schemas = components["schemas"];

/**
 * What one line's per diem day is: which kind of day, which meals somebody
 * else paid for, and the dated figures it was priced from. The amount is the
 * line's own `grossAmount` — this package never works one out.
 */
export type PerDiem = Omit<Schemas["ExpensesEntryPerDiem"], "type"> & { type: PerDiemType };

export type PerDiemMealPercents = Schemas["ExpensesEntryMealPercents"];

/** The four kinds of per diem day (design §4), in the order the state agreement lists them. */
export const perDiemTypes = ["day_6_12", "day_over_12", "overnight_hotel", "overnight_other"] as const;

export type PerDiemType = (typeof perDiemTypes)[number];

/**
 * The `expenses` catalog key naming a kind of day. A per diem line carries no
 * description of its own — the server stores the empty string and says so in
 * the contract — so this is what names it, in the reader's own language.
 */
export const perDiemTypeLabelKey = (type: PerDiemType): string => `perDiemType_${type}`;

/** The three meals, in the order they are eaten. */
export const meals = ["breakfast", "lunch", "dinner"] as const;

export type Meal = (typeof meals)[number];

export const mealLabelKey = (meal: Meal): string => `meal_${meal}`;

export interface MealDeduction {
  meal: Meal;
  covered: boolean;
  /**
   * The percentage of the day rate the meal deducts, as the table priced it
   * on this day. **Absent rather than zero** when the table held none and the
   * meal was not covered: a deduction of nothing and a deduction nobody has
   * set are different facts, and `0 %` would be a figure the server never
   * said.
   */
  percent?: number;
}

const coveredFlag = (perDiem: PerDiem, meal: Meal): boolean =>
  meal === "breakfast" ? perDiem.breakfastCovered : meal === "lunch" ? perDiem.lunchCovered : perDiem.dinnerCovered;

/** One row per meal, in eating order, for the row a traveller ticks. */
export const mealDeductions = (perDiem: PerDiem): MealDeduction[] =>
  meals.map((meal) => ({
    meal,
    covered: coveredFlag(perDiem, meal),
    ...(perDiem.mealPercents[meal] !== undefined ? { percent: perDiem.mealPercents[meal] } : {}),
  }));
