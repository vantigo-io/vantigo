import { describe, expect, it } from "vitest";
import golden from "./brand-scale.golden.json";
import {
  BRAND_ALGORITHM,
  BrandScaleError,
  CARD_DARK,
  CARD_LIGHT,
  contrast,
  contrastChecks,
  darkTint,
  filledText,
  generateScale,
  lightness,
  PAGE_DARK,
  PAGE_LIGHT,
  PAIRS,
  passes,
  suggestPrimary,
} from "./scale";

const round2 = (v: number) => Math.floor(v * 100 + 0.5) / 100;

describe("the golden file, which the Go package writes and tests too", () => {
  it("is the version this port implements, with the pairs in the order it checks them", () => {
    expect(golden.algorithm).toBe(BRAND_ALGORITHM);
    expect(golden.pairs).toEqual([...PAIRS]);
    expect(golden.cases).toHaveLength(10);
  });

  for (const c of golden.cases) {
    it(`gives ${c.name} (${c.input}) the same scale, text, ratios and suggestion as Go`, () => {
      const result = generateScale(c.input);
      expect([...result.scale]).toEqual(c.scale);
      expect(result.filledText).toBe(c.filledText);
      const checks = contrastChecks(result);
      expect(checks.filter((k) => k.scheme === "light").map((k) => round2(k.ratio))).toEqual(c.contrast.light);
      expect(checks.filter((k) => k.scheme === "dark").map((k) => round2(k.ratio))).toEqual(c.contrast.dark);
      expect(suggestPrimary(c.input)).toBe(c.suggested);
    });
  }

  for (const r of golden.refused) {
    it(`refuses ${r.input} as ${r.error}`, () => {
      expect(() => generateScale(r.input)).toThrow(BrandScaleError);
      try {
        generateScale(r.input);
      } catch (e) {
        expect((e as BrandScaleError).code).toBe(r.error);
      }
    });
  }
});

describe("the properties of the scale", () => {
  for (const c of golden.cases) {
    it(`holds for ${c.name}`, () => {
      const { scale } = generateScale(c.input);
      expect(scale[6]).toBe(c.input);
      let previous = 2;
      for (const shade of scale) {
        expect(shade).toMatch(/^#[0-9a-f]{6}$/);
        const l = lightness(shade);
        expect(l).toBeLessThan(previous);
        previous = l;
      }
    });
  }

  it("takes an upper-case input and keeps shade 6 lower-case", () => {
    expect(generateScale("#4F46E5").scale[6]).toBe("#4f46e5");
  });

  it("refuses a primary out of the lightness range and one that is not #rrggbb", () => {
    for (const [hex, code] of [
      ["#ffffff", "brand_primary_range"],
      ["#000000", "brand_primary_range"],
      ["#4f46e", "brand_hex"],
      ["4f46e5", "brand_hex"],
      ["#4f46eg", "brand_hex"],
      ["", "brand_hex"],
    ] as const) {
      expect(() => generateScale(hex)).toThrow(expect.objectContaining({ code }));
    }
  });
});

describe("the filled text and the contrast", () => {
  it("flips at the luminance threshold of 0.179, and one of the two always passes AA", () => {
    expect(filledText("#757575")).toBe("#ffffff");
    expect(filledText("#767676")).toBe("#000000");
    expect(filledText("#000000")).toBe("#ffffff");
    expect(filledText("#ffffff")).toBe("#000000");
    for (let v = 0; v < 256; v++) {
      const bg = `#${v.toString(16).padStart(2, "0").repeat(3)}`;
      expect(contrast(filledText(bg), bg)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("is WCAG's ratio", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 9);
    expect(contrast("#4f46e5", "#4f46e5")).toBe(1);
    expect(contrast("#4f46e5", "#ffffff")).toBe(contrast("#ffffff", "#4f46e5"));
  });

  it("halves shade 9 for the dark tint, rounding half up", () => {
    expect(darkTint("#000000")).toBe("#000000");
    expect(darkTint("#ffffff")).toBe("#808080");
    expect(darkTint("#0102ff")).toBe("#010180");
  });

  it("checks the ten pairs of B4", () => {
    const result = generateScale("#4f46e5");
    const checks = contrastChecks(result);
    const s = result.scale;
    expect(checks.map((k) => [k.scheme, k.pair, k.foreground, k.background])).toEqual([
      ["light", "filled", "#ffffff", s[6]],
      ["light", "filledHover", "#ffffff", s[7]],
      ["light", "linkPage", s[8], PAGE_LIGHT],
      ["light", "linkCard", s[8], CARD_LIGHT],
      ["light", "linkTint", s[8], s[0]],
      ["dark", "filled", "#ffffff", s[6]],
      ["dark", "filledHover", "#ffffff", s[7]],
      ["dark", "linkPage", s[3], PAGE_DARK],
      ["dark", "linkCard", s[3], CARD_DARK],
      ["dark", "linkTint", s[3], darkTint(s[9])],
    ]);
    expect(passes(checks)).toBe(true);
  });
});

describe("the suggested primary", () => {
  it("is the input when it passes, and otherwise a primary that does", () => {
    expect(suggestPrimary("#4f46e5")).toBe("#4f46e5");
    const grey = suggestPrimary("#808080");
    expect(grey).not.toBeNull();
    expect(lightness(grey as string)).toBeLessThan(lightness("#808080"));
    const green = suggestPrimary("#16a34a");
    expect(lightness(green as string)).toBeGreaterThan(lightness("#16a34a"));
    expect(suggestPrimary("#ffffff")).toBeNull();
  });
});
