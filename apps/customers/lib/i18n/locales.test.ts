import { describe, expect, it } from "vitest";
import { defaultLocale, isLocale, negotiateLocale } from "./locales";

describe("negotiateLocale", () => {
  it("returns default locale for null header", () => {
    expect(negotiateLocale(null)).toBe(defaultLocale);
  });

  it("returns default locale for empty header", () => {
    expect(negotiateLocale("")).toBe(defaultLocale);
  });

  it("picks Norwegian for nb-NO", () => {
    expect(negotiateLocale("nb-NO,nb;q=0.9,en;q=0.8")).toBe("nb");
  });

  it("maps no to nb", () => {
    expect(negotiateLocale("no")).toBe("nb");
  });

  it("maps nn-NO to nb", () => {
    expect(negotiateLocale("nn-NO")).toBe("nb");
  });

  it("picks English for en-US", () => {
    expect(negotiateLocale("en-US,en;q=0.9")).toBe("en");
  });

  it("respects q-value ordering", () => {
    expect(negotiateLocale("en;q=0.5,nb;q=0.9")).toBe("nb");
  });

  it("falls back to default for unsupported languages", () => {
    expect(negotiateLocale("de-DE,fr;q=0.8")).toBe("en");
  });

  it("handles garbage input", () => {
    expect(negotiateLocale(";;;,,q=")).toBe("en");
  });

  it("excludes q=0 entries", () => {
    expect(negotiateLocale("nb;q=0,en")).toBe("en");
  });
});

describe("isLocale", () => {
  it("accepts supported locales", () => {
    expect(isLocale("en")).toBe(true);
    expect(isLocale("nb")).toBe(true);
  });

  it("rejects unsupported values", () => {
    expect(isLocale("no")).toBe(false);
    expect(isLocale("")).toBe(false);
    expect(isLocale(42)).toBe(false);
    expect(isLocale(null)).toBe(false);
    expect(isLocale(undefined)).toBe(false);
  });
});
