import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { CARD_DARK, CARD_LIGHT, PAGE_DARK, PAGE_LIGHT, TEXT_DARK, TEXT_LIGHT } from "./scale";

// The Go package keeps the same constants and has the same test.
describe("the token constants", () => {
  it("are the page, card and text colours of theme.css", () => {
    const css = readFileSync(`${(import.meta as unknown as { dirname: string }).dirname}/../theme.css`, "utf8");
    const dark = css.indexOf('[data-mantine-color-scheme="dark"]');
    expect(dark).toBeGreaterThan(0);
    const value = (block: string, name: string) =>
      block.match(new RegExp(`--vantigo-${name}:\\s*(#[0-9a-fA-F]{6})\\s*;`))?.[1]?.toLowerCase();
    expect(value(css.slice(0, dark), "body-bg")).toBe(PAGE_LIGHT);
    expect(value(css.slice(0, dark), "surface")).toBe(CARD_LIGHT);
    expect(value(css.slice(0, dark), "text")).toBe(TEXT_LIGHT);
    expect(value(css.slice(dark), "body-bg")).toBe(PAGE_DARK);
    expect(value(css.slice(dark), "surface")).toBe(CARD_DARK);
    expect(value(css.slice(dark), "text")).toBe(TEXT_DARK);
  });
});
