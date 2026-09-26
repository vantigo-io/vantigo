import { describe, expect, it } from "vitest";
import { documentTotals, lineAmounts } from "./money";

// The server's own hand-computed cases (drafts_test.go TestDrafts_TheMoney),
// so the editor's live totals and the saved draft cannot disagree.
describe("the editor's money", () => {
  it("reads 0.1 × 3 as exactly 0.30", () => {
    expect(lineAmounts(3, 0.1, 0)).toEqual({ gross: 0.3, allowance: 0, net: 0.3 });
  });

  it("takes a discount as an allowance of the rounded gross", () => {
    expect(lineAmounts(3, 33.33, 10)).toEqual({ gross: 99.99, allowance: 10, net: 89.99 });
  });

  it("computes VAT per rate on the sum of the nets, not per line", () => {
    const third = { net: lineAmounts(1, 33.33, 0).net, category: "S", ratePercent: 25 };
    const totals = documentTotals([third, third, third]);
    // Per line it would be 3 × 8.33 = 24.99.
    expect(totals).toEqual({
      rates: [{ category: "S", ratePercent: 25, taxable: 99.99, vat: 25 }],
      net: 99.99,
      vat: 25,
      gross: 124.99,
    });
  });

  it("gives every rate its row, a 0 % category included, highest rate first", () => {
    const totals = documentTotals([
      { net: 50.5, category: "Z", ratePercent: 0 },
      { net: 200, category: "S", ratePercent: 15 },
      { net: 100.01, category: "S", ratePercent: 25 },
    ]);
    expect(totals.rates.map((r) => [r.category, r.ratePercent, r.taxable, r.vat])).toEqual([
      ["S", 25, 100.01, 25],
      ["S", 15, 200, 30],
      ["Z", 0, 50.5, 0],
    ]);
    expect(totals.gross).toBe(405.51);
  });
});
