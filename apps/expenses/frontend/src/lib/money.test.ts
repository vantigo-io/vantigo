import { describe, expect, it } from "vitest";
import { netOf, round2, vatFromGross } from "./money";

describe("vatFromGross", () => {
  it("takes the VAT out of a gross at the rate, half-up to two places", () => {
    expect(vatFromGross(625, 25)).toBe(125);
    expect(vatFromGross(1150, 15)).toBe(150);
    expect(vatFromGross(1120, 12)).toBe(120);
  });

  it("rounds a share that does not divide evenly", () => {
    // 100 × 25 / 125 = 20 exactly; 99.99 × 25 / 125 = 19.998 → 20.00
    expect(vatFromGross(99.99, 25)).toBe(20);
    expect(vatFromGross(37.5, 12)).toBe(4.02);
  });
});

describe("netOf", () => {
  it("is the gross less the VAT", () => {
    expect(netOf(625, 125)).toBe(500);
    expect(netOf(100.1, 20.02)).toBe(80.08);
  });
});

describe("round2", () => {
  it("rounds a value the binary representation left a hair under, up", () => {
    expect(round2(1.005)).toBe(1.01);
    expect(round2(2.675)).toBe(2.68);
  });
});
