/**
 * D5's arithmetic in the browser, exact: every amount is scaled to an integer
 * (BigInt) at its column's decimals, multiplied, and rounded once to øre with
 * the half away from zero — the server's rule — so the live totals the editor
 * shows are the ones the server will compute, to the øre. The server stays
 * authoritative; this only spares the person a round trip per keystroke.
 */

/** v at `places` decimals as an integer: 12.5 at 2 is 1250n. */
export const scaled = (v: number, places: number): bigint => {
  if (!Number.isFinite(v)) return 0n;
  return BigInt(v.toFixed(places).replace(".", ""));
};

/** n / d rounded to the nearest integer, the half away from zero. */
const divRound = (n: bigint, d: bigint): bigint => {
  const q = n / d;
  const r = n % d;
  const twice = 2n * (r < 0n ? -r : r);
  if (twice >= d) return q + (n < 0n ? -1n : 1n);
  return q;
};

const fromOre = (ore: bigint): number => Number(ore) / 100;

export interface LineAmounts {
  gross: number;
  allowance: number;
  net: number;
}

/** A line's gross, its discount as an allowance of the rounded gross, and its net. */
export const lineAmounts = (quantity: number, unitPrice: number, discountPercent: number): LineAmounts => {
  const gross = divRound(scaled(quantity, 3) * scaled(unitPrice, 4), 100000n);
  const allowance = divRound(gross * scaled(discountPercent, 2), 10000n);
  return { gross: fromOre(gross), allowance: fromOre(allowance), net: fromOre(gross - allowance) };
};

export interface TaxedLine {
  net: number;
  category: string;
  ratePercent: number;
}

export interface RateTotal {
  category: string;
  ratePercent: number;
  taxable: number;
  vat: number;
}

export interface DocumentTotals {
  rates: RateTotal[];
  net: number;
  vat: number;
  gross: number;
}

/**
 * VAT per (category, rate) on the sum of the lines' nets (Peppol BR-CO-17),
 * never per line; the rows highest rate first, then by category — the order
 * the server answers them in.
 */
export const documentTotals = (lines: TaxedLine[]): DocumentTotals => {
  const groups = new Map<string, { category: string; ratePercent: number; taxable: bigint }>();
  for (const line of lines) {
    const key = `${line.category}|${line.ratePercent.toFixed(2)}`;
    const group = groups.get(key) ?? { category: line.category, ratePercent: line.ratePercent, taxable: 0n };
    group.taxable += scaled(line.net, 2);
    groups.set(key, group);
  }
  let net = 0n;
  let vat = 0n;
  const rates: RateTotal[] = [];
  for (const group of groups.values()) {
    const groupVat = divRound(group.taxable * scaled(group.ratePercent, 2), 10000n);
    net += group.taxable;
    vat += groupVat;
    rates.push({
      category: group.category,
      ratePercent: group.ratePercent,
      taxable: fromOre(group.taxable),
      vat: fromOre(groupVat),
    });
  }
  rates.sort((a, b) => b.ratePercent - a.ratePercent || a.category.localeCompare(b.category));
  return { rates, net: fromOre(net), vat: fromOre(vat), gross: fromOre(net + vat) };
};
