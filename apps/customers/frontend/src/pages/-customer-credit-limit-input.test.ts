import { describe, expect, it } from "vitest";
import { parseCreditLimit } from "./-customer-billing-modal";

describe("parseCreditLimit", () => {
  it("reads spaces as thousands and a comma or a point as the decimal", () => {
    expect(parseCreditLimit("100000")).toBe("100000");
    expect(parseCreditLimit("1 000,50")).toBe("1000.50");
    expect(parseCreditLimit("1 000.5")).toBe("1000.5");
  });

  it("is null when empty", () => {
    expect(parseCreditLimit("  ")).toBeNull();
  });

  it("refuses what it cannot read for sure", () => {
    expect(parseCreditLimit("1,000.00")).toBeUndefined();
    expect(parseCreditLimit("1.000,00")).toBeUndefined();
    expect(parseCreditLimit("1,000")).toBeUndefined();
    expect(parseCreditLimit("-5")).toBeUndefined();
    expect(parseCreditLimit("ti tusen")).toBeUndefined();
  });
});
