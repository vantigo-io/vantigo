import { describe, expect, it } from "vitest";
import { createVantigoTheme, vantigoIndigo, vantigoTheme } from "../theme";
import golden from "./brand-tokens.golden.json";
import { BRAND_RADII, type BrandRadius, BUILTIN_SHADES, builtinPayload, computeTokens, RADIUS_VALUES } from "./tokens";

interface GoldenBrand {
  name: string;
  primary: string;
  radius: string;
  tokens: unknown;
}

const brands = golden.brands as unknown as GoldenBrand[];

const radiusKey = (value: string): BrandRadius => {
  const key = BRAND_RADII.find((r) => RADIUS_VALUES[r] === value);
  if (!key) throw new Error(`unknown radius ${value}`);
  return key;
};

// The server writes the golden file (identity's brand_tokens_test.go, UPDATE_GOLDEN=1); this port must
// generate the same bytes for every brand in it.
describe("the brand tokens", () => {
  it("has the golden brands, including Vantigo's own", () => {
    expect(brands.length).toBeGreaterThanOrEqual(5);
    expect(brands.some((b) => b.primary.toLowerCase() === "#4f46e5")).toBe(true);
  });

  for (const b of brands) {
    it(`match the server's for ${b.name} (${b.primary})`, () => {
      const tokens = computeTokens(b.primary.toLowerCase(), radiusKey((b.tokens as { radius: string }).radius));
      expect(JSON.parse(JSON.stringify(tokens))).toEqual(b.tokens);
    });
  }
});

describe("the default brand", () => {
  it("keeps the ten shades the product shipped with", () => {
    expect(BUILTIN_SHADES).toEqual(vantigoIndigo);
    expect(builtinPayload().tokens.light.shades).toEqual(vantigoIndigo);
  });

  it("is today's theme exactly", () => {
    expect(createVantigoTheme(builtinPayload())).toEqual(vantigoTheme);
    expect(createVantigoTheme()).toEqual(vantigoTheme);
    expect(vantigoTheme.colors?.vantigo).toEqual(vantigoIndigo);
    expect(vantigoTheme.primaryShade).toEqual({ light: 6, dark: 6 });
    expect(vantigoTheme.defaultRadius).toBe("md");
  });

  it("sets the auto-contrast threshold the server chooses the filled text with", () => {
    expect(vantigoTheme.autoContrast).toBe(true);
    expect(vantigoTheme.luminanceThreshold).toBe(0.179);
  });
});

describe("a brand's theme", () => {
  it("carries the brand's shades and radius, and Vantigo's dark neutrals", () => {
    const tokens = computeTokens("#0f766e", "xl");
    const theme = createVantigoTheme({ tokens });
    expect(theme.colors?.vantigo).toEqual(tokens.light.shades);
    expect(theme.colors?.vantigo?.[6]).toBe("#0f766e");
    expect(theme.defaultRadius).toBe("xl");
    expect(theme.colors?.dark).toEqual(vantigoTheme.colors?.dark);
  });
});
