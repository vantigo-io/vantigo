import { describe, expect, it } from "vitest";
import { resolveLocale } from "./locale";

describe("resolveLocale", () => {
  it("uses an explicit supported preference", () => {
    expect(resolveLocale("en", ["nb-NO"])).toBe("en");
    expect(resolveLocale("nb", ["en-US"])).toBe("nb");
  });

  it("resolves automatic preference from browser language order", () => {
    expect(resolveLocale("auto", ["fr-FR", "nb-NO", "en-US"])).toBe("nb");
    expect(resolveLocale("auto", ["fr-FR", "en-US"])).toBe("en");
  });

  it("falls back to English when no browser language is supported", () => {
    expect(resolveLocale("auto", ["fr-FR", "de-DE"])).toBe("en");
  });

  it("falls back to English when the runtime reports no languages", () => {
    expect(resolveLocale("auto", [])).toBe("en");
  });
});
