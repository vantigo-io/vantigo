import { describe, expect, it } from "vitest";
import { ordersCatalog } from "./i18n";
import { ordersNavItems } from "./lib/navigation";

describe("the orders catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(ordersCatalog.nb).sort()).toEqual(Object.keys(ordersCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(ordersCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("names every sidebar destination", () => {
    for (const item of ordersNavItems) expect(ordersCatalog.en).toHaveProperty(item.label);
  });
});
