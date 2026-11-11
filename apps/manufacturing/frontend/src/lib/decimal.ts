/**
 * Exact decimal text, as Manufacturing keeps every quantity, time and amount
 * (manufacturing design §11): never a float from the field to the request.
 * What the person types is normalised (a comma is a decimal point, spaces
 * group thousands) and checked as text; arithmetic, where the screen needs
 * any, is done on scaled integers.
 */

/** At most three decimals, as quantities are stored. */
export const QUANTITY_DECIMALS = 3;

/** What the person typed as the contract's decimal text: no spaces, a point for the decimal separator. */
export const normalizeDecimal = (text: string): string => text.replace(/\s/g, "").replace(",", ".");

const DECIMAL = /^(\d+)(?:\.(\d+))?$/;

/** Whether the text is a decimal of 0 or more with at most `maxDecimals` decimals, once normalised. */
export const isDecimal = (text: string, maxDecimals = QUANTITY_DECIMALS): boolean => {
  const match = DECIMAL.exec(normalizeDecimal(text));
  return match !== null && (match[2]?.length ?? 0) <= maxDecimals;
};

/** The decimal as an integer of its value × 10^scale; the text must be a decimal with at most `scale` decimals. */
const scaled = (text: string, scale: number): bigint => {
  const match = DECIMAL.exec(normalizeDecimal(text));
  if (!match) throw new Error(`not a decimal: ${text}`);
  const fraction = (match[2] ?? "").padEnd(scale, "0");
  return BigInt(match[1] + fraction.slice(0, scale));
};

/** Whether the decimal is more than 0. */
export const isPositiveDecimal = (text: string): boolean => /[1-9]/.test(normalizeDecimal(text)) && isDecimal(text, 99);

/** The comparison of two decimals: negative, zero or positive as `a` is below, at or above `b`. */
export const compareDecimal = (a: string, b: string): number => {
  const scale = Math.max(
    normalizeDecimal(a).split(".")[1]?.length ?? 0,
    normalizeDecimal(b).split(".")[1]?.length ?? 0,
  );
  const difference = scaled(a, scale) - scaled(b, scale);
  return difference === 0n ? 0 : difference < 0n ? -1 : 1;
};

/** The larger number of decimals of the two decimals. */
const commonScale = (a: string, b: string): number =>
  Math.max(normalizeDecimal(a).split(".")[1]?.length ?? 0, normalizeDecimal(b).split(".")[1]?.length ?? 0);

/** A scaled integer of 0 or more as exact decimal text with no trailing zeros. */
const unscaled = (value: bigint, scale: number): string => {
  const raw = value.toString().padStart(scale + 1, "0");
  const whole = scale > 0 ? raw.slice(0, -scale) : raw;
  const fraction = scale > 0 ? raw.slice(-scale).replace(/0+$/, "") : "";
  return fraction ? `${whole}.${fraction}` : whole;
};

/** `a` plus `b`, as exact decimal text with no trailing zeros. */
export const addDecimal = (a: string, b: string): string => {
  const scale = commonScale(a, b);
  return unscaled(scaled(a, scale) + scaled(b, scale), scale);
};

/** `a` less `b`, never below 0, as exact decimal text with no trailing zeros (`2.5` less `0.5` → `2`). */
export const subtractDecimal = (a: string, b: string): string => {
  const scale = commonScale(a, b);
  const difference = scaled(a, scale) - scaled(b, scale);
  return difference <= 0n ? "0" : unscaled(difference, scale);
};

/** The decimal rounded half-up to `places` decimals, as text with exactly that many (`12.345` → `12.35`). */
export const roundDecimal = (text: string, places: number): string => {
  const normal = normalizeDecimal(text);
  const negative = normal.startsWith("-");
  const digits = negative ? normal.slice(1) : normal;
  const decimals = digits.split(".")[1]?.length ?? 0;
  const scale = Math.max(decimals, places);
  let value = scaled(digits, scale);
  const drop = BigInt(10) ** BigInt(scale - places);
  if (drop > 1n) value = (value + drop / 2n) / drop;
  const raw = value.toString().padStart(places + 1, "0");
  const whole = places > 0 ? raw.slice(0, -places) : raw;
  const fraction = places > 0 ? raw.slice(-places) : "";
  return `${negative && value !== 0n ? "-" : ""}${whole}${fraction ? `.${fraction}` : ""}`;
};

/**
 * The decimal as the viewer reads it: thousands grouped and the decimal
 * separator of their language, every digit kept (`1234.5` → `1 234,5` in
 * Norwegian). Text that is not a decimal is shown as it came.
 */
export const formatDecimal = (text: string, locale: string): string => {
  const normal = normalizeDecimal(text);
  const negative = normal.startsWith("-");
  const match = DECIMAL.exec(negative ? normal.slice(1) : normal);
  if (!match) return text;
  const format = new Intl.NumberFormat(locale);
  const separator = format.formatToParts(1.5).find((part) => part.type === "decimal")?.value ?? ".";
  const whole = format.format(BigInt(match[1]));
  return `${negative ? "-" : ""}${whole}${match[2] ? `${separator}${match[2]}` : ""}`;
};
