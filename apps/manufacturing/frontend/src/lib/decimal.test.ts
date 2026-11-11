import { describe, expect, it } from "vitest";
import { compareDecimal, formatDecimal, isDecimal, isPositiveDecimal, normalizeDecimal, roundDecimal } from "./decimal";

describe("decimal text", () => {
  it("normalises what the person typed", () => {
    expect(normalizeDecimal(" 1 234,5 ")).toBe("1234.5");
    expect(normalizeDecimal("0.125")).toBe("0.125");
  });

  it("checks decimals as text, at most three decimals by default", () => {
    expect(isDecimal("0.125")).toBe(true);
    expect(isDecimal("2,5")).toBe(true);
    expect(isDecimal("0.1234")).toBe(false);
    expect(isDecimal("-1")).toBe(false);
    expect(isDecimal("1e3")).toBe(false);
    expect(isDecimal("")).toBe(false);
    expect(isPositiveDecimal("0.000")).toBe(false);
    expect(isPositiveDecimal("0.001")).toBe(true);
  });

  it("compares and rounds without floats", () => {
    expect(compareDecimal("99.99", "99.990")).toBe(0);
    expect(compareDecimal("100", "99.99")).toBe(1);
    expect(compareDecimal("0.1", "0.25")).toBe(-1);
    expect(roundDecimal("12.345", 2)).toBe("12.35");
    expect(roundDecimal("12.344", 2)).toBe("12.34");
    expect(roundDecimal("5", 2)).toBe("5.00");
    expect(roundDecimal("0.004", 2)).toBe("0.00");
    expect(roundDecimal("0.1234567890123456789", 2)).toBe("0.12");
  });

  it("shows every digit in the viewer's language", () => {
    expect(formatDecimal("1234.5", "en")).toBe("1,234.5");
    expect(formatDecimal("1234.5", "nb").replace(/\s/g, " ")).toBe("1 234,5");
    expect(formatDecimal("12345678901234567890.125", "en")).toBe("12,345,678,901,234,567,890.125");
    expect(formatDecimal("n/a", "en")).toBe("n/a");
  });
});
