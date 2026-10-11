import { describe, expect, it } from "vitest";
import { staffCatalog } from "./i18n";
import { staffNavItems } from "./lib/navigation";
import {
  appointmentFormsFor,
  employeeTypes,
  employmentTypes,
  noticeUnits,
  relationshipTypes,
  remunerationTypes,
  temporaryGrounds,
  workingHoursSchemes,
} from "./lib/terms";

const placeholders = (text: string): string[] => [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort();

describe("the staff catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(staffCatalog.nb).sort()).toEqual(Object.keys(staffCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(staffCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("interpolates the same values in both languages", () => {
    const en = staffCatalog.en as Record<string, string>;
    const nb = staffCatalog.nb as Record<string, string>;
    for (const key of Object.keys(en)) {
      expect(placeholders(nb[key] ?? ""), `nb.${key}`).toEqual(placeholders(en[key]));
    }
  });

  it("names every value of every enumeration a form offers, and every sidebar destination", () => {
    const en = staffCatalog.en as Record<string, string>;
    const keys = [
      ...employmentTypes.map((value) => `employmentType_${value}`),
      ...temporaryGrounds.map((value) => `temporaryGround_${value}`),
      ...[...appointmentFormsFor("fast"), ...appointmentFormsFor("midlertidig")].map(
        (value) => `appointmentForm_${value}`,
      ),
      ...workingHoursSchemes.map((value) => `workingHoursScheme_${value}`),
      ...relationshipTypes.map((value) => `relationshipType_${value}`),
      ...employeeTypes.map((value) => `employeeType_${value}`),
      ...noticeUnits.map((value) => `noticeUnit_${value}`),
      ...remunerationTypes.map((value) => `remunerationType_${value}`),
      ...staffNavItems.map((item) => item.label),
    ];
    for (const key of keys) expect(en[key], `no label for ${key}`).toBeDefined();
  });

  it("explains the presumption in both languages, naming the section of the act", () => {
    expect(staffCatalog.en.presumptionWarning).toMatch(/aml\. § 14-6/);
    expect(staffCatalog.nb.presumptionWarning).toMatch(/aml\. § 14-6/);
  });
});
