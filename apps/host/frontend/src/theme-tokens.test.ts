import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// The semantic colour tokens live in packages/frontend-shell/src/theme.css, as
// literal values, because they must be in the stylesheet before any script runs
// (no flash of the wrong scheme on load). The dark scale lives in theme.ts.
// This keeps the two equal. It reads the files, so it lives here, in the
// workspace whose tests run under Node's types.
const shellSrc = join(dirname(fileURLToPath(import.meta.url)), "../../../../packages/frontend-shell/src");
const css = readFileSync(join(shellSrc, "theme.css"), "utf8");
const themeTs = readFileSync(join(shellSrc, "theme.ts"), "utf8");

/** The ten hex values of `vantigoDark` in theme.ts. */
const darkScale = [...(/vantigoDark[^=]*=\s*\[([^\]]+)\]/.exec(themeTs)?.[1] ?? "").matchAll(/"(#[0-9a-f]{6})"/g)].map(
  (m) => m[1],
);

/** The declarations of one rule in theme.css, by its selector. */
const rule = (selector: string): Record<string, string> => {
  const start = css.indexOf(`${selector} {`);
  expect(start, `theme.css has no ${selector} rule`).toBeGreaterThan(-1);
  const body = css.slice(css.indexOf("{", start) + 1, css.indexOf("}", start));
  return Object.fromEntries(
    [...body.matchAll(/(--[\w-]+|color-scheme)\s*:\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]),
  );
};

describe("theme.css", () => {
  const light = rule(":root");
  const dark = rule(':root[data-mantine-color-scheme="dark"]');

  it("defines every token the shell exports as vantigoTokens, for light", () => {
    const exported = [...themeTs.matchAll(/"var\((--vantigo-[\w-]+)\)"/g)].map((m) => m[1]);
    expect(exported).toHaveLength(8);
    for (const name of exported) expect(light, `${name} in light`).toHaveProperty([name]);
  });

  it("restates the tokens that differ for dark; dimmed text and borders follow Mantine's own variables", () => {
    for (const name of [
      "--vantigo-body-bg",
      "--vantigo-surface",
      "--vantigo-raised",
      "--vantigo-text",
      "--vantigo-link",
      "--vantigo-subtle-bg",
    ]) {
      expect(dark, `${name} in dark`).toHaveProperty([name]);
    }
    expect(light["--vantigo-text-dimmed"]).toBe("var(--mantine-color-dimmed)");
    expect(light["--vantigo-border"]).toBe("var(--mantine-color-default-border)");
  });

  it("keeps light as it was and tells the browser which scheme is which", () => {
    expect(light["--vantigo-body-bg"]).toBe("#f7f8fc");
    expect(light["--vantigo-surface"]).toBe("#ffffff");
    expect(light["--vantigo-text"]).toBe("#182033");
    expect(light["color-scheme"]).toBe("light");
    expect(dark["color-scheme"]).toBe("dark");
  });

  it("restates the dark scale's page, surface and text values", () => {
    expect(darkScale).toHaveLength(10);
    expect(dark["--vantigo-body-bg"]).toBe(darkScale[8]);
    expect(dark["--vantigo-surface"]).toBe(darkScale[7]);
    // What floats above a card is one step lighter in dark; in light it is the surface.
    expect(dark["--vantigo-raised"]).toBe(darkScale[6]);
    expect(light["--vantigo-raised"]).toBe(light["--vantigo-surface"]);
    expect(dark["--vantigo-text"]).toBe(darkScale[0]);
    // The brand's shade 3: shade 8 is too dark for a link on dark. A brand changes it (brands design B2).
    expect(dark["--vantigo-link"]).toBe("var(--mantine-color-vantigo-3)");
    expect(light["--vantigo-link"]).toBe("var(--mantine-color-vantigo-8)");
    expect(light["--vantigo-subtle-bg"]).toBe("var(--mantine-color-vantigo-0)");
  });

  it("makes a card the surface in dark, and what floats the raised surface", () => {
    // Mantine's own Card in dark is the fill of an input and of a dropdown: a field in a card would not show.
    const rule = (selector: string) =>
      new RegExp(
        `:root\\[data-mantine-color-scheme="dark"\\] \\.${selector}[^{]*\\{\\s*background-color: var\\((--vantigo-[a-z]+)\\)`,
      ).exec(css)?.[1];
    expect(rule("mantine-Card-root")).toBe("--vantigo-surface");
    expect(rule("mantine-Combobox-dropdown")).toBe("--vantigo-raised");
  });
});
