import { describe, expect, it } from "bun:test";
import { validateCatalog } from "./validator";

describe("i18n catalog validation", () => {
  it("compares nested key sets exactly", () => {
    const issues = validateCatalog("exampleCatalog", {
      en: { account: { title: "Account", description: "Details" } },
      nb: { account: { title: "Konto", extra: "Ekstra" } },
    });

    expect(issues.map((issue) => issue.message)).toEqual([
      "Missing key in nb: account.description",
      "Missing key in en: account.extra",
    ]);
  });

  it("rejects empty strings and mismatched interpolation names", () => {
    const issues = validateCatalog("exampleCatalog", {
      en: { greeting: "Hello {{name}}", empty: "" },
      nb: { greeting: "Hei", empty: " " },
    });

    expect(issues.map((issue) => issue.kind)).toEqual(["empty-string", "empty-string", "interpolation-mismatch"]);
  });

  it("accepts matching nested catalogs and interpolation names", () => {
    const issues = validateCatalog("exampleCatalog", {
      en: { greeting: "Hello {{name}}", nested: { count: "{{count}} item" } },
      nb: { greeting: "Hei {{name}}", nested: { count: "{{count}} element" } },
    });

    expect(issues).toEqual([]);
  });
});
