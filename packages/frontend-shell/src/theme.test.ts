import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { contrast } from "./brand/scale";
import {
  COLOR_SCHEME_STORAGE_KEY,
  vantigoColorSchemeManager,
  vantigoCssVariablesResolver,
  vantigoDark,
  vantigoTheme,
} from "./theme";

describe("the colour scheme manager", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => window.localStorage.clear());

  it("mirrors the choice under the key the pre-paint snippet reads", () => {
    expect(COLOR_SCHEME_STORAGE_KEY).toBe("vantigo-color-scheme");
    vantigoColorSchemeManager.set("dark");
    expect(window.localStorage.getItem("vantigo-color-scheme")).toBe("dark");
    expect(vantigoColorSchemeManager.get("auto")).toBe("dark");
  });

  it("falls back to the default when nothing is stored", () => {
    expect(vantigoColorSchemeManager.get("auto")).toBe("auto");
  });
});

describe("the theme", () => {
  it("replaces Mantine's dark scale and keeps indigo filled at shade 6 in both schemes", () => {
    expect(vantigoTheme.colors?.dark).toEqual(vantigoDark);
    expect(vantigoDark).toHaveLength(10);
    expect(vantigoTheme.primaryShade).toEqual({ light: 6, dark: 6 });
  });
});

describe("the dark scheme's elevation and tint", () => {
  it("makes each step lighter than the one it lies on, and keeps text readable on all three", () => {
    const [text, , dimmed, , , , raised, surface, page] = vantigoDark;
    expect(contrast(surface, page)).toBeGreaterThan(1.1);
    expect(contrast(raised, surface)).toBeGreaterThan(1.1);
    for (const background of [page, surface, raised]) {
      expect(contrast(text, background)).toBeGreaterThan(7);
      expect(contrast(dimmed, background)).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("gives Mantine a brand tint that is lighter than the card, and a neutral one for grey", () => {
    const vars = vantigoCssVariablesResolver(vantigoTheme as never).dark;
    const tint = vars["--mantine-color-vantigo-light"] as string;
    expect(tint).toBe("#2b2d55");
    expect(contrast(tint, vantigoDark[7])).toBeGreaterThan(1.25);
    expect(contrast(vantigoTheme.colors?.vantigo?.[1] as string, tint)).toBeGreaterThan(7);
    expect(vars["--mantine-color-vantigo-light-color"]).toBe("var(--mantine-color-vantigo-1)");
    expect(vars["--mantine-color-gray-light"]).toBe("var(--mantine-color-dark-5)");
    expect(vantigoCssVariablesResolver(vantigoTheme as never).light).toEqual({});
  });
});
