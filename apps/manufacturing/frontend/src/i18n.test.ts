import { describe, expect, it } from "vitest";
import manufacturingYaml from "../../../../openapi/manufacturing.yaml?raw";
import { refusalCodes } from "./api/request";
import { statuses, statusFilters, workingStates } from "./api/work-centers";
import { manufacturingCatalog } from "./i18n";
import { manufacturingNavItems } from "./lib/navigation";
import { weekdays } from "./lib/pattern";

const placeholders = (text: string): string[] => [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort();

/** The values of the contract's enum for a schema. */
const enumOf = (schema: string): string[] => {
  const from = manufacturingYaml.indexOf(`        ${schema}:\n`);
  expect(from, `the contract has ${schema}`).toBeGreaterThan(-1);
  const rest = manufacturingYaml.slice(manufacturingYaml.indexOf("enum:", from) + "enum:".length);
  const values: string[] = [];
  for (const line of rest.split("\n").slice(1)) {
    const value = /^\s+- (.+)$/.exec(line);
    if (!value) break;
    values.push(value[1].replace(/^['"]|['"]$/g, ""));
  }
  return values;
};

describe("the manufacturing catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(manufacturingCatalog.nb).sort()).toEqual(Object.keys(manufacturingCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(manufacturingCatalog))
      for (const [key, text] of Object.entries(catalog))
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
  });

  it("interpolates the same values in both languages", () => {
    const en = manufacturingCatalog.en as Record<string, string>;
    const nb = manufacturingCatalog.nb as Record<string, string>;
    for (const key of Object.keys(en)) expect(placeholders(nb[key] ?? ""), `nb.${key}`).toEqual(placeholders(en[key]));
  });

  it("uses the trade's Norwegian names (M7)", () => {
    expect(manufacturingCatalog.nb.manufacturing).toBe("Produksjon");
    expect([
      manufacturingCatalog.nb.navOverview,
      manufacturingCatalog.nb.navOrders,
      manufacturingCatalog.nb.navShopFloor,
      manufacturingCatalog.nb.navSchedule,
      manufacturingCatalog.nb.navBoms,
      manufacturingCatalog.nb.navWorkCenters,
    ]).toEqual(["Oversikt", "Produksjonsordrer", "Verksted", "Produksjonsplan", "Stykklister", "Arbeidsstasjoner"]);
  });

  it("names every status, state, weekday, problem, refusal and sidebar destination", () => {
    const keys = [
      ...statuses.map((value) => `status_${value}`),
      ...statusFilters.map((value) => `statusFilter_${value}`),
      ...workingStates.map((value) => `workingState_${value}`),
      ...weekdays.map((value) => `weekday_${value}`),
      ...["grid", "order", "overlap", "tooMany"].map((value) => `patternProblem_${value}`),
      ...[
        "required",
        "quantity",
        "notNegative",
        "scrap",
        "minutes",
        "setupMinutes",
        "codeLength",
        "codeHint",
        "capacityHint",
        "timeEfficiencyHint",
      ].map((value) => `problem_${value}`),
      ...refusalCodes.map((value) => `refusal.${value}`),
      ...manufacturingNavItems.map((item) => item.label),
    ];
    for (const [language, catalog] of Object.entries(manufacturingCatalog))
      for (const key of keys)
        expect((catalog as Record<string, string>)[key], `${language}: no label for ${key}`).toBeDefined();
  });

  it("keeps the enumerations in step with the contract", () => {
    expect([...statuses].sort()).toEqual(enumOf("ManufacturingStatus").sort());
    expect([...statusFilters].sort()).toEqual(enumOf("ManufacturingStatusFilter").sort());
    expect([...workingStates].sort()).toEqual(enumOf("ManufacturingWorkingState").sort());
  });

  it("words only refusals the contract names", () => {
    for (const code of refusalCodes) expect(manufacturingYaml, `the contract names ${code}`).toContain(code);
  });
});
