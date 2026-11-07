import { describe, expect, it } from "vitest";
import { knowledgeCatalog } from "./i18n";
import { knowledgeNavItems } from "./lib/navigation";

describe("the knowledge catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(knowledgeCatalog.nb).sort()).toEqual(Object.keys(knowledgeCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(knowledgeCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("names every sidebar destination", () => {
    for (const item of knowledgeNavItems) expect(knowledgeCatalog.en).toHaveProperty(item.label);
  });
});
