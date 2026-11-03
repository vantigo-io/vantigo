import { describe, expect, it } from "vitest";
import inventoryYaml from "../../../../openapi/inventory.yaml?raw";
import { countStatuses, uncountedChoices } from "./api/counts";
import { importOutcomes } from "./api/imports";
import { labelLayouts } from "./api/labels";
import { locationKinds } from "./api/locations";
import { adjustmentModes, adjustmentReasons, movementKinds, refusalCodes } from "./api/movements";
import { siteKinds, statuses } from "./api/sites";
import { inventoryCatalog } from "./i18n";
import { inventoryNavItems } from "./lib/navigation";

const placeholders = (text: string): string[] => [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort();

/** The values of the contract's enum for a schema — or for one property of it. */
const enumOf = (schema: string, property?: string): string[] => {
  let from = inventoryYaml.indexOf(`        ${schema}:\n`);
  expect(from, `the contract has ${schema}`).toBeGreaterThan(-1);
  if (property) from = inventoryYaml.indexOf(`${property}:`, from);
  const rest = inventoryYaml.slice(inventoryYaml.indexOf("enum:", from) + "enum:".length);
  const values: string[] = [];
  for (const line of rest.split("\n").slice(1)) {
    const value = /^\s+- (.+)$/.exec(line);
    if (!value) break;
    values.push(value[1].replace(/^['"]|['"]$/g, ""));
  }
  return values;
};

describe("the inventory catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(inventoryCatalog.nb).sort()).toEqual(Object.keys(inventoryCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(inventoryCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("interpolates the same values in both languages", () => {
    const en = inventoryCatalog.en as Record<string, string>;
    const nb = inventoryCatalog.nb as Record<string, string>;
    for (const key of Object.keys(en)) {
      expect(placeholders(nb[key] ?? ""), `nb.${key}`).toEqual(placeholders(en[key]));
    }
  });

  it("names every kind, status, reason, mode, refusal, outcome, layout and sidebar destination", () => {
    const keys = [
      ...siteKinds.map((value) => `siteKind_${value}`),
      ...locationKinds.map((value) => `locationKind_${value}`),
      ...statuses.map((value) => `status_${value}`),
      ...movementKinds.map((value) => `kind_${value}`),
      // A count is posted from its counting screen, not from a new-movement
      // form, so only the three posting kinds have the form's words.
      ...movementKinds
        .filter((value) => value !== "count")
        .flatMap((value) => [
          `new_${value}`,
          `newDescription_${value}`,
          `notAllowed_${value}`,
          `post_${value}`,
          `posted_${value}`,
        ]),
      ...adjustmentReasons.map((value) => `reason_${value}`),
      ...adjustmentModes.map((value) => `mode_${value}`),
      ...["location", "variant"].map((value) => `matchKind_${value}`),
      ...["variant", "quantity", "location"].map((value) => `lineGap_${value}`),
      ...refusalCodes.map((value) => `refusal.${value}`),
      ...countStatuses.map((value) => `countStatus_${value}`),
      ...uncountedChoices.map((value) => `uncounted_${value}`),
      ...importOutcomes.map((value) => `importOutcome_${value}`),
      ...labelLayouts.map((value) => `labelLayout_${value}`),
      ...inventoryNavItems.map((item) => item.label),
    ];
    for (const [language, catalog] of Object.entries(inventoryCatalog))
      for (const key of keys)
        expect((catalog as Record<string, string>)[key], `${language}: no label for ${key}`).toBeDefined();
  });

  it("keeps the enumerations in step with the contract", () => {
    expect([...siteKinds].sort()).toEqual(enumOf("InventorySiteKind").sort());
    expect([...locationKinds].sort()).toEqual(enumOf("InventoryLocationKind").sort());
    expect([...statuses].sort()).toEqual(enumOf("InventoryStatus").sort());
    expect([...movementKinds].sort()).toEqual(enumOf("InventoryMovementKind").sort());
    expect([...adjustmentReasons].sort()).toEqual(enumOf("InventoryAdjustmentReason").sort());
    expect([...adjustmentModes].sort()).toEqual(enumOf("InventoryAdjustmentLine", "mode").sort());
    expect(["location", "variant"]).toEqual(enumOf("InventoryLookupMatch", "kind").sort());
    expect([...countStatuses].sort()).toEqual(enumOf("InventoryCountStatus").sort());
    expect([...uncountedChoices].sort()).toEqual(enumOf("InventoryUncounted").sort());
    expect([...importOutcomes].sort()).toEqual(enumOf("InventoryImportOutcome").sort());
    expect([...labelLayouts].sort()).toEqual(enumOf("InventoryLabelLayout").sort());
  });

  it("words every refusal the contract names", () => {
    for (const code of refusalCodes) expect(inventoryYaml, `the contract names ${code}`).toContain(code);
  });
});
