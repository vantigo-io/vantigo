import { readFileSync } from "node:fs";
import { MantineProvider } from "@mantine/core";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { createVantigoTheme, vantigoCssVariablesResolver, vantigoTheme } from "../theme";
import golden from "./brand-tokens.golden.json";
import { type BrandTokens, builtinPayload, RADIUS_VALUES } from "./tokens";

interface GoldenBrand {
  name: string;
  primary: string;
  tokens: BrandTokens;
}

const brands = golden.brands as unknown as GoldenBrand[];

/** Every declaration of every block whose selector contains `selector`, from the style tags Mantine injects. */
const declarations = (selector: string): Record<string, string> => {
  const css = Array.from(document.querySelectorAll("style[data-mantine-styles]"))
    .map((s) => s.textContent ?? "")
    .join("\n");
  const out: Record<string, string> = {};
  for (const block of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!(block[1] as string).includes(selector)) continue;
    for (const decl of (block[2] as string).split(";")) {
      const at = decl.indexOf(":");
      if (at > 0) out[decl.slice(0, at).trim()] = decl.slice(at + 1).trim();
    }
  }
  return out;
};

const MANTINE_DEFAULTS: Record<string, string> = {
  "--mantine-primary-color-contrast": "var(--mantine-color-white)",
  "--mantine-radius-default": RADIUS_VALUES.md,
};

const mount = (tokens: BrandTokens) =>
  render(
    <MantineProvider
      env="test"
      theme={createVantigoTheme({ tokens })}
      cssVariablesResolver={vantigoCssVariablesResolver}
    >
      <span />
    </MantineProvider>,
  );

// B36: the server generates the variables Mantine derives from the shades, with the shell's own rule for
// the dark tint (vantigoCssVariablesResolver); a Mantine upgrade that changes a rule must fail here instead
// of drifting.
describe("the brand payload against Mantine", () => {
  for (const b of brands) {
    it(`derives the variables the server generated for ${b.name}`, () => {
      const { unmount } = mount(b.tokens);
      const root = declarations(":root:root");
      const shared = { ...declarations(":root"), ...root };
      b.tokens.light.shades.forEach((shade, i) => {
        expect(shared[`--mantine-color-vantigo-${i}`], `shade ${i}`).toBe(shade);
      });
      // Mantine leaves out a variable whose value is its own default (white contrast text, the md radius).
      expect(shared["--mantine-radius-default"] ?? MANTINE_DEFAULTS["--mantine-radius-default"]).toBe(b.tokens.radius);
      const light = declarations('[data-mantine-color-scheme="light"]');
      const dark = declarations('[data-mantine-color-scheme="dark"]');
      for (const [name, value] of Object.entries(b.tokens.light.derived)) {
        expect(light[name] ?? MANTINE_DEFAULTS[name], `light ${name}`).toBe(value);
      }
      for (const [name, value] of Object.entries(b.tokens.dark.derived)) {
        expect(dark[name] ?? MANTINE_DEFAULTS[name], `dark ${name}`).toBe(value);
      }
      unmount();
    });
  }
});

// B2: --vantigo-link and --vantigo-subtle-bg come from the brand's shades, not from literals.
describe("theme.css against a non-default brand", () => {
  const css = readFileSync(`${(import.meta as unknown as { dirname: string }).dirname}/../theme.css`, "utf8");
  const dark = css.indexOf('[data-mantine-color-scheme="dark"]');
  const value = (block: string, name: string) => block.match(new RegExp(`--vantigo-${name}:\\s*([^;]+);`))?.[1]?.trim();

  /** Resolves `var(--mantine-color-vantigo-N)` against the shades Mantine declared for the brand. */
  const resolve = (v: string | undefined, shades: Record<string, string>): string | undefined => {
    const name = v?.match(/^var\((--[a-z0-9-]+)\)$/)?.[1];
    return name ? shades[name] : v;
  };

  it("defines the link and the tint as the shades 8, 3 and 0", () => {
    expect(value(css.slice(0, dark), "link")).toBe("var(--mantine-color-vantigo-8)");
    expect(value(css.slice(0, dark), "subtle-bg")).toBe("var(--mantine-color-vantigo-0)");
    expect(value(css.slice(dark), "link")).toBe("var(--mantine-color-vantigo-3)");
    expect(value(css.slice(dark), "subtle-bg")).toBe("var(--mantine-color-vantigo-light)");
  });

  it("changes the computed link and tint with the brand", () => {
    const resolved = (tokens: BrandTokens) => {
      const { unmount } = mount(tokens);
      const vars = { ...declarations(":root"), ...declarations(":root:root") };
      const darkDeclared = declarations('[data-mantine-color-scheme="dark"]');
      const result = {
        lightLink: resolve(value(css.slice(0, dark), "link"), vars),
        lightTint: resolve(value(css.slice(0, dark), "subtle-bg"), vars),
        darkLink: resolve(value(css.slice(dark), "link"), vars),
        darkTint: resolve(value(css.slice(dark), "subtle-bg"), { ...vars, ...darkDeclared }),
      };
      unmount();
      return result;
    };
    const base = resolved(builtinPayload().tokens);
    const teal = brands.find((b) => b.primary.toLowerCase() !== "#4f46e5") as GoldenBrand;
    const other = resolved(teal.tokens);
    expect(base.lightLink).toBe("#3730a3");
    expect(base.lightTint).toBe("#eef2ff");
    expect(base.darkLink).toBe("#a5b4fc");
    expect(other.lightLink).toBe(teal.tokens.light.shades[8]);
    expect(other.lightTint).toBe(teal.tokens.light.shades[0]);
    expect(other.darkLink).toBe(teal.tokens.dark.shades[3]);
    expect(other.darkTint).toBe(teal.tokens.dark.derived["--mantine-color-vantigo-light"]);
    expect(other.lightLink).not.toBe(base.lightLink);
  });

  it("keeps the default theme's variables", () => {
    const { unmount } = render(
      <MantineProvider env="test" theme={vantigoTheme}>
        <span />
      </MantineProvider>,
    );
    expect(declarations(":root")["--mantine-color-vantigo-6"]).toBe("#4f46e5");
    unmount();
  });
});
