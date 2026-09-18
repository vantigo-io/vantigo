import { describe, expect, it } from "vitest";
import { projectsCatalog } from "../i18n";
import { billingTypeLabelKey, billingTypes, pricingModeLabelKey, pricingModes } from "./billing";

describe("billingTypeLabelKey", () => {
  it("names a key the catalog carries in both languages", () => {
    for (const billingType of billingTypes) {
      const key = billingTypeLabelKey(billingType);
      expect(projectsCatalog.en).toHaveProperty(key);
      expect(projectsCatalog.nb).toHaveProperty(key);
    }
  });
});

describe("pricingModeLabelKey", () => {
  it("names a key the catalog carries in both languages", () => {
    for (const mode of pricingModes) {
      const key = pricingModeLabelKey(mode);
      expect(projectsCatalog.en).toHaveProperty(key);
      expect(projectsCatalog.nb).toHaveProperty(key);
    }
  });
});
