import { describe, expect, it } from "vitest";
import accountingYaml from "../../../../openapi/accounting.yaml?raw";
import { reportKinds, systemRoles, voucherKinds } from "./api/accounting";
import { refusalCodes } from "./api/request";
import { accountingCatalog } from "./i18n";
import { accountingNavItems } from "./lib/navigation";

const placeholders = (text: string): string[] => [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort();

/** The values of the contract's enum for a schema. */
const enumOf = (schema: string): string[] => {
  const from = accountingYaml.indexOf(`        ${schema}:\n`);
  expect(from, `the contract has ${schema}`).toBeGreaterThan(-1);
  const rest = accountingYaml.slice(accountingYaml.indexOf("enum:", from) + "enum:".length);
  const values: string[] = [];
  for (const line of rest.split("\n").slice(1)) {
    const value = /^\s+- (.+)$/.exec(line);
    if (!value) break;
    values.push(value[1].replace(/^['"]|['"]$/g, ""));
  }
  return values;
};

describe("the accounting catalog", () => {
  const en = accountingCatalog.en as Record<string, string>;
  const nb = accountingCatalog.nb as Record<string, string>;

  it("has every key in both languages, and no other", () => {
    expect(Object.keys(nb).sort()).toEqual(Object.keys(en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(accountingCatalog))
      for (const [key, text] of Object.entries(catalog))
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
  });

  it("interpolates the same values in both languages", () => {
    for (const key of Object.keys(en)) expect(placeholders(nb[key] ?? ""), `nb.${key}`).toEqual(placeholders(en[key]));
  });

  it("names every value of the contract's enums", () => {
    expect([...voucherKinds].sort()).toEqual(enumOf("AccountingVoucherKind").sort());
    expect([...systemRoles].sort()).toEqual(enumOf("AccountingSystemRole").sort());
    expect([...reportKinds].sort()).toEqual(enumOf("AccountingReportKind").sort());
    for (const kind of voucherKinds) expect(en[`voucherKind_${kind}`], kind).toBeDefined();
    for (const role of systemRoles) expect(en[`role_${role}`], role).toBeDefined();
    for (const report of reportKinds) expect(en[`report_${report}`], report).toBeDefined();
    for (const kind of enumOf("AccountingAccountKind")) expect(en[`kind_${kind}`], kind).toBeDefined();
    for (const term of enumOf("AccountingVatTerm")) expect(en[`vatTerm_${term}`], term).toBeDefined();
    for (const direction of enumOf("AccountingTaxDirection"))
      expect(en[`direction_${direction}`], direction).toBeDefined();
  });

  it("has words for every refusal the contract names", () => {
    const description = accountingYaml.slice(accountingYaml.indexOf("AccountingProblem:"));
    for (const code of refusalCodes) {
      expect(description, code).toContain(`${code} (`);
      expect(en[`refusal.${code}`], code).toBeDefined();
    }
  });

  it("has a label for every sidebar entry", () => {
    for (const item of accountingNavItems) expect(en[item.label], item.label).toBeDefined();
  });

  it("uses the trade's Norwegian words", () => {
    expect([nb.accounting, nb.navVouchers, nb.navAccounts, nb.navOpenItems, nb.lockDate, nb.openingBalances]).toEqual([
      "Regnskap",
      "Bilag",
      "Kontoplan",
      "Åpne poster",
      "Sperredato",
      "Inngående balanse",
    ]);
  });
});
