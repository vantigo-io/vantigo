/** Amounts as whole øre: the bank's forms add and compare øre, never floats. */

/** An amount's øre, exactly, from the contract's decimal text. */
export const oreOf = (text: string): number => {
  const negative = text.trim().startsWith("-");
  const [whole, fraction = ""] = text.trim().replace("-", "").split(".");
  const ore = Number(whole) * 100 + Number(`${fraction}00`.slice(0, 2));
  return negative ? -ore : ore;
};

/** Øre as the contract's decimal text. */
export const decimalOf = (ore: number): string => {
  const sign = ore < 0 ? "-" : "";
  const abs = Math.abs(ore);
  return `${sign}${Math.floor(abs / 100)}.${String(abs % 100).padStart(2, "0")}`;
};
