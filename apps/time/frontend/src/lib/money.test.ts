import { describe, expect, it } from "vitest";
import { billedAmount } from "./money";

describe("billedAmount", () => {
  it("bills hours at the rate times the multiplier, rounded once", () => {
    // 333.33 × 1.5 h × 150 % = 749.9925. Rounding the rate first
    // (499.995 → 500.00) would bill 750.00, which no other surface says.
    expect(billedAmount(333.33, 1.5, 150)).toBe(749.99);
  });

  it("is the plain rate over the hours without a work type", () => {
    expect(billedAmount(1200, 7.5)).toBe(9000);
    expect(billedAmount(1200, 7.5, null)).toBe(9000);
  });

  it("rounds a half cent up", () => {
    // 0.01 × 0.5 h = 0.005
    expect(billedAmount(0.01, 0.5)).toBe(0.01);
  });
});
