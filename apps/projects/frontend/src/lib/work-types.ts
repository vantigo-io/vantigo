/**
 * The server's rules for a work type (work types design D1), mirrored so the
 * form refuses what the API would, in the same words and by the same count.
 */

/** The name column's width, in characters. */
export const MAX_WORK_TYPE_NAME = 100;

/** Ten times the rate: the design's ceiling, not the column's. */
export const MAX_MULTIPLIER_PERCENT = 1000;

/**
 * A name's length as the server counts it: in characters (Go's rune count),
 * not in UTF-16 code units, so an emoji is one character and not two.
 */
export const nameLength = (name: string): number => Array.from(name).length;

/** The server's three multiplier messages, in the order it checks them. */
export const multiplierProblems = ["multiplierNotPositive", "multiplierTooLarge", "multiplierTooPrecise"] as const;
export type MultiplierProblem = (typeof multiplierProblems)[number];

/**
 * Which of the server's multiplier rules a value breaks, first one first —
 * more than zero (an empty field is zero to the server), at most 1000 %, at
 * most two decimals — or null. The form's `decimalScale` already stops a
 * third decimal being typed; the rule is here so the two sides say the same.
 */
export const multiplierProblem = (value: number | string): MultiplierProblem | null => {
  const parsed = typeof value === "number" ? value : value.trim() === "" ? 0 : Number(value.trim());
  if (Number.isNaN(parsed) || parsed <= 0) return "multiplierNotPositive";
  if (parsed > MAX_MULTIPLIER_PERCENT) return "multiplierTooLarge";
  // Compared in hundredths with a tolerance: 0.29 × 100 is 28.999… in binary
  // floating point and still has two decimals.
  const hundredths = parsed * 100;
  if (Math.abs(hundredths - Math.round(hundredths)) > 1e-6) return "multiplierTooPrecise";
  return null;
};
