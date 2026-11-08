import { describe, expect, it } from "vitest";
import { agentsCatalog } from "./i18n";
import { agentsNavItems } from "./lib/navigation";

// The package's own source, tests and generated files aside.
const sources = import.meta.glob(["./**/*.{ts,tsx}", "!./**/*.test.{ts,tsx}", "!./test/**", "!./api-schema.d.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/** A plural key is written as its stem; the catalog holds the `_one` and `_other` forms. */
const inCatalog = (key: string) =>
  key in agentsCatalog.en || (`${key}_one` in agentsCatalog.en && `${key}_other` in agentsCatalog.en);

describe("the agents catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(agentsCatalog.nb).sort()).toEqual(Object.keys(agentsCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(agentsCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("keeps each key's placeholders the same in both languages", () => {
    const placeholders = (text: string) => [...text.matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]).sort();
    for (const [key, text] of Object.entries(agentsCatalog.en))
      expect(placeholders(agentsCatalog.nb[key as keyof typeof agentsCatalog.nb]), key).toEqual(placeholders(text));
  });

  it("names every sidebar destination", () => {
    for (const item of agentsNavItems) expect(agentsCatalog.en).toHaveProperty(item.label);
  });

  it("has words for every key the source asks for by name", () => {
    const missing = new Set<string>();
    for (const [file, text] of Object.entries(sources)) {
      for (const match of text.matchAll(/\bt\("([A-Za-z0-9_.]+)"/g))
        if (!inCatalog(match[1])) missing.add(`${file}: ${match[1]}`);
    }
    expect([...missing]).toEqual([]);
  });

  it("words every state, level, mode, hand-off warning, template and hard rule of the contract", () => {
    const families: Record<string, readonly string[]> = {
      level: ["off", "read", "change"],
      replyMode: ["review", "auto_above", "auto"],
      handOffWarning: ["disabled", "no_permission", "not_a_person"],
      template: ["front_desk"],
      rule: ["S1", "S2", "S3", "S4", "S5", "S6", "S7", "C1", "C2"],
    };
    for (const [family, values] of Object.entries(families))
      for (const value of values) expect(agentsCatalog.en, `${family}.${value}`).toHaveProperty([`${family}.${value}`]);
  });
});
