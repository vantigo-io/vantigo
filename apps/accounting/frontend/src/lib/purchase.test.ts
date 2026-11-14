import { describe, expect, it } from "vitest";
import { fromOre, lineTotals, rateOf, reverseChargeVAT, toNOK, toOre } from "./purchase";

// The supplier invoice form's live totals, to the øre, as the server computes
// them: per line, half away from zero.

const input = (rate: string) => ({ direction: "input" as const, reverseCharge: false, rate });

describe("øre", () => {
  it("reads and writes exact amounts", () => {
    expect(toOre("1250")).toBe(125000n);
    expect(toOre("0,5")).toBe(50n);
    expect(toOre("-12.34")).toBe(-1234n);
    expect(toOre("1.234")).toBeNull();
    expect(toOre("abc")).toBeNull();
    expect(fromOre(125000n)).toBe("1250.00");
    expect(fromOre(-5n)).toBe("-0.05");
    expect(fromOre(0n)).toBe("0.00");
  });
});

describe("a line's VAT", () => {
  it("takes it out of a gross amount, or adds it to a net one, at each rate", () => {
    expect(lineTotals(125000n, true, input("25.00"))).toEqual({ net: 100000n, vat: 25000n, gross: 125000n });
    expect(lineTotals(9999n, true, input("25.00"))).toEqual({ net: 7999n, vat: 2000n, gross: 9999n });
    expect(lineTotals(100000n, false, input("15.00"))).toEqual({ net: 100000n, vat: 15000n, gross: 115000n });
    expect(lineTotals(100000n, false, input("11.11"))).toEqual({ net: 100000n, vat: 11110n, gross: 111110n });
    expect(lineTotals(2n, false, input("25.00"))).toEqual({ net: 2n, vat: 1n, gross: 3n });
  });

  it("carries none on a reverse-charge code, a code of no VAT treatment or no code", () => {
    expect(lineTotals(100000n, false, { direction: "input", reverseCharge: true, rate: "25.00" }).vat).toBe(0n);
    expect(lineTotals(100000n, false, { direction: "none", reverseCharge: false, rate: "25.00" }).vat).toBe(0n);
    expect(lineTotals(100000n, true, null)).toEqual({ net: 100000n, vat: 0n, gross: 100000n });
    expect(reverseChargeVAT(115000n, "25.00")).toBe(28750n);
  });
});

describe("another currency", () => {
  it("converts at the rate and finds the rate from a NOK total", () => {
    expect(toNOK(10000n, "11.5")).toBe(115000n);
    expect(toNOK(1332n, "11.1111")).toBe(14800n);
    expect(rateOf(123456n, 10000n)).toBe("12.345600");
    expect(rateOf(1n, 0n)).toBeNull();
  });
});
