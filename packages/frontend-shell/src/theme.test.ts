import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { COLOR_SCHEME_STORAGE_KEY, vantigoColorSchemeManager, vantigoDark, vantigoTheme } from "./theme";

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
