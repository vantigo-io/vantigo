import { describe, expect, it } from "vitest";
import { projectsCatalog } from "../i18n";
import { multiplierProblem, multiplierProblems, nameLength } from "./work-types";

describe("multiplierProblem", () => {
  it("names the server's three rules, one at a time", () => {
    expect(multiplierProblem("")).toBe("multiplierNotPositive");
    expect(multiplierProblem(0)).toBe("multiplierNotPositive");
    expect(multiplierProblem(-5)).toBe("multiplierNotPositive");
    expect(multiplierProblem(1000.01)).toBe("multiplierTooLarge");
    expect(multiplierProblem(150.125)).toBe("multiplierTooPrecise");
    expect(multiplierProblem("150.125")).toBe("multiplierTooPrecise");
    expect(multiplierProblem(1000)).toBeNull();
    expect(multiplierProblem(0.01)).toBeNull();
    expect(multiplierProblem(137.5)).toBeNull();
    // 0.29 × 100 is 28.999… in binary floating point; it still has two decimals.
    expect(multiplierProblem(0.29)).toBeNull();
  });
});

describe("multiplierProblems", () => {
  it("names keys the catalog carries in both languages", () => {
    for (const key of multiplierProblems) {
      expect(projectsCatalog.en).toHaveProperty(key);
      expect(projectsCatalog.nb).toHaveProperty(key);
    }
  });
});

describe("nameLength", () => {
  it("counts characters as the server's rune count does, not UTF-16 units", () => {
    expect(nameLength("Helg")).toBe(4);
    expect(nameLength("🌙🌙")).toBe(2);
  });
});
