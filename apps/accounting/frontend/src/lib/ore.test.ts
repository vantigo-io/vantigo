import { describe, expect, it } from "vitest";
import { decimalOf, oreOf } from "./ore";

describe("øre", () => {
  it("reads the contract's decimals exactly and writes them back", () => {
    expect(oreOf("-349.00")).toBe(-34900);
    expect(oreOf("0.1")).toBe(10);
    expect(oreOf("1234.56")).toBe(123456);
    expect(decimalOf(-34900)).toBe("-349.00");
    expect(decimalOf(5)).toBe("0.05");
    expect(decimalOf(oreOf("21512.00"))).toBe("21512.00");
  });
});
