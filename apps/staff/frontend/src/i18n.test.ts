import { describe, expect, it } from "vitest";
import staffYaml from "../../../../openapi/staff.yaml?raw";
import eventsGo from "../../../server/internal/staff/events.go?raw";
import { staffCatalog } from "./i18n";
import { eventFieldLabelKey, eventFields, eventKinds } from "./lib/events";
import { staffNavItems } from "./lib/navigation";
import { myPageTabs, personTabs } from "./lib/search";
import {
  appointmentFormsFor,
  bankAccountKinds,
  contractCheckItems,
  employeeTypes,
  employmentTypes,
  endReasons,
  genders,
  nationalIdKinds,
  nextOfKinRelationships,
  noticeUnits,
  payPeriods,
  personRoleKinds,
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
      ...genders.map((value) => `gender_${value}`),
      ...staffNavItems.map((item) => item.label),
      ...endReasons.map((value) => `endReason_${value}`),
      ...payPeriods.map((value) => `payPeriod_${value}`),
      ...nextOfKinRelationships.map((value) => `relationship_${value}`),
      ...personRoleKinds.map((value) => `role_${value}`),
      ...nationalIdKinds.map((value) => `nationalIdKind_${value}`),
      ...bankAccountKinds.map((value) => `bankAccountKind_${value}`),
      ...contractCheckItems.map((value) => `contractItem_${value}`),
      ...["present", "missing", "not_applicable"].map((value) => `contractState_${value}`),
      ...personTabs.map((value) => `tab_${value}`),
      ...myPageTabs.map((value) => `tab_${value}`),
      ...eventKinds.map((value) => `event_${value}`),
    ];
    for (const key of keys) expect(en[key], `no label for ${key}`).toBeDefined();
  });

  it("words every refusal of absence the contract names, in both languages", () => {
    const codes = [
      "absence_overlap",
      "absence_note_on_sick",
      "absence_not_requested",
      "absence_not_cancellable",
      "absence_status_change",
      "percent_not_allowed",
      "hours_on_day_type",
      "carry_over_recorded",
      "own_request",
      "self_certification_exhausted",
      "self_certification_too_early",
      "self_certification_withdrawn",
      "holiday_postponement_not_applicable",
      "holiday_postponement_too_late",
      "revision_conflict",
      "person_erased",
    ];
    for (const code of codes) {
      expect(staffYaml, `the contract names ${code}`).toContain(code);
      for (const [language, catalog] of Object.entries(staffCatalog))
        expect((catalog as Record<string, string>)[`refusal.${code}`], `${language} refusal.${code}`).toBeDefined();
    }
  });

  it("knows every kind of event the server writes, and no other", () => {
    const written = [...eventsGo.matchAll(/^\s*event\w+\s*=\s*"(\w+)"/gm)].map((m) => m[1]).sort();
    expect(written.length).toBeGreaterThan(0);
    expect([...eventKinds].sort()).toEqual(written);
  });

  it("names every field a history entry can name", () => {
    const en = staffCatalog.en as Record<string, string>;
    for (const field of eventFields) expect(en[eventFieldLabelKey(field)], `no label for ${field}`).toBeDefined();
  });

  it("names seventeen items of the contract check, a to q", () => {
    expect(contractCheckItems).toHaveLength(17);
    expect(contractCheckItems.join("")).toBe("abcdefghijklmnopq");
  });

  it("explains the presumption in both languages, naming the section of the act", () => {
    expect(staffCatalog.en.presumptionWarning).toMatch(/aml\. § 14-6/);
    expect(staffCatalog.nb.presumptionWarning).toMatch(/aml\. § 14-6/);
  });
});
