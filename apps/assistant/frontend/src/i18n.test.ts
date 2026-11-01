import { describe, expect, it } from "vitest";
import { assistantCatalog } from "./i18n";

const placeholders = (value: string) => [...value.matchAll(/{{\s*(\w+)\s*}}/g)].map((match) => match[1]).sort();

describe("the assistant catalog", () => {
  const en = assistantCatalog.en as Record<string, string>;
  const nb = assistantCatalog.nb as Record<string, string>;

  it("has the same keys in English and Norwegian bokmål", () => {
    expect(Object.keys(nb).sort()).toEqual(Object.keys(en).sort());
  });

  it("has no empty text, and the same placeholders in both languages", () => {
    for (const key of Object.keys(en)) {
      expect(en[key].trim(), `en ${key}`).not.toBe("");
      expect(nb[key].trim(), `nb ${key}`).not.toBe("");
      expect(placeholders(nb[key]), `placeholders of ${key}`).toEqual(placeholders(en[key]));
    }
  });

  it("names the app Assistent in Norwegian", () => {
    expect(en.assistant).toBe("Assistant");
    expect(nb.assistant).toBe("Assistent");
  });
});
