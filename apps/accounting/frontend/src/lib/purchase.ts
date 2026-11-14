/**
 * A supplier invoice's live totals on the form (A18): each line's net, VAT
 * and gross in integer øre, computed as the server computes them — per line,
 * docmath's rounding, half away from zero — so the form shows what Post will
 * book. An amount is never a float: text in, øre (bigint) through, text out.
 */

/** An exact amount as øre; null when the text is not one (at most two decimals). */
export const toOre = (text: string): bigint | null => {
  const trimmed = text.trim().replace(",", ".");
  const match = /^(-?)(\d{1,12})(?:\.(\d{1,2}))?$/.exec(trimmed);
  if (!match) return null;
  const [, sign, whole, fraction = ""] = match;
  const ore = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, "0"));
  return sign === "-" ? -ore : ore;
};

/** Øre as an exact amount's text, `1234.50`. */
export const fromOre = (ore: bigint): string => {
  const negative = ore < 0n;
  const abs = negative ? -ore : ore;
  const whole = abs / 100n;
  const fraction = (abs % 100n).toString().padStart(2, "0");
  return `${negative && abs !== 0n ? "-" : ""}${whole}.${fraction}`;
};

/** A rate's text (`25.00`, `11.11`) as hundredths of a percent. */
const rateHundredths = (rate: string): bigint | null => {
  const match = /^(\d{1,2})(?:\.(\d{1,2}))?$/.exec(rate.trim());
  if (!match) return null;
  return BigInt(match[1]) * 100n + BigInt((match[2] ?? "").padEnd(2, "0"));
};

/** numerator / denominator rounded half away from zero, both bigint. */
const divRound = (numerator: bigint, denominator: bigint): bigint => {
  const negative = numerator < 0n !== denominator < 0n;
  const n = numerator < 0n ? -numerator : numerator;
  const d = denominator < 0n ? -denominator : denominator;
  const q = (n * 2n + d) / (d * 2n);
  return negative ? -q : q;
};

/** How a line's tax code treats the VAT: the server's own reading of its direction and flags. */
export interface LineCode {
  direction: "input" | "output" | "none";
  reverseCharge: boolean;
  /** The rate on the invoice's date, `25.00`; null when none covers it. */
  rate: string | null;
}

export interface LineTotals {
  net: bigint;
  vat: bigint;
  gross: bigint;
}

/**
 * One line's net, VAT and gross in øre: with an input code and a rate, the VAT
 * from the net (`net × rate / 100`) or out of the gross
 * (`gross × rate / (100 + rate)`); a reverse-charge code carries no VAT on the
 * invoice, and neither does a code of no VAT treatment.
 */
export const lineTotals = (amount: bigint, gross: boolean, code: LineCode | null): LineTotals => {
  if (code?.direction !== "input" || code.reverseCharge || code.rate === null)
    return { net: amount, vat: 0n, gross: amount };
  const rate = rateHundredths(code.rate);
  if (rate === null || rate === 0n) return { net: amount, vat: 0n, gross: amount };
  if (gross) {
    const vat = divRound(amount * rate, 10000n + rate);
    return { net: amount - vat, vat, gross: amount };
  }
  const vat = divRound(amount * rate, 10000n);
  return { net: amount, vat, gross: amount + vat };
};

/** The reverse-charge VAT the buyer calculates on a NOK base, in øre. */
export const reverseChargeVAT = (baseNOK: bigint, rate: string | null): bigint => {
  const hundredths = rate ? rateHundredths(rate) : null;
  return hundredths ? divRound(baseNOK * hundredths, 10000n) : 0n;
};

/**
 * A total in the invoice's currency at a rate (NOK per unit, up to six
 * decimals), in NOK øre; null when the rate is not a number.
 */
export const toNOK = (ore: bigint, rate: string): bigint | null => {
  const match = /^(\d{1,6})(?:\.(\d{1,6}))?$/.exec(rate.trim().replace(",", "."));
  if (!match) return null;
  const micro = BigInt(match[1]) * 1_000_000n + BigInt((match[2] ?? "").padEnd(6, "0"));
  return divRound(ore * micro, 1_000_000n);
};

/** The rate NOK per unit, six decimals, that a NOK total and a currency total make. */
export const rateOf = (totalNOK: bigint, total: bigint): string | null => {
  if (total === 0n) return null;
  const micro = divRound(totalNOK * 1_000_000n, total);
  const whole = micro / 1_000_000n;
  return `${whole}.${(micro % 1_000_000n).toString().padStart(6, "0")}`;
};
