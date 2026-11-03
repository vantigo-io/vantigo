import { describe, expect, it } from "vitest";
import { ELECTRICITY_TAX_CLASSES, ELECTRICITY_TAX_REASONS } from "../api/energy";
import { energyCatalog } from "../i18n";
import { taxClassKey, taxReasonKey } from "./billing";

const has = (key: string) =>
  (["en", "nb"] as const).every((language) => key in (energyCatalog[language] as Record<string, string>));

describe("taxClassKey and taxReasonKey", () => {
  it("name every known class and reason with a key both languages hold", () => {
    for (const value of ELECTRICITY_TAX_CLASSES) expect(has(taxClassKey(value))).toBe(true);
    for (const reason of ELECTRICITY_TAX_REASONS) expect(has(taxReasonKey(reason))).toBe(true);
    expect(taxClassKey("reduced")).toBe("electricityTaxClassReduced");
    expect(taxReasonKey("industry")).toBe("electricityTaxReason_industry");
  });

  it("fall back to a generic label and sentence for a class or reason the client does not know", () => {
    expect(taxClassKey("half")).toBe("electricityTaxClassUnknown");
    expect(taxReasonKey("battery")).toBe("electricityTaxReasonUnknown");
    expect(has("electricityTaxClassUnknown")).toBe(true);
    expect(has("electricityTaxReasonUnknown")).toBe(true);
  });
});
