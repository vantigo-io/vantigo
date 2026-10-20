import { describe, expect, it } from "vitest";
import staffYaml from "../../../../openapi/staff.yaml?raw";
import identityProvisionerGo from "../../../server/internal/contracts/identity_provisioner.go?raw";
import accountFlagGo from "../../../server/internal/staff/account_flag.go?raw";
import accountsDecideGo from "../../../server/internal/staff/accounts_decide.go?raw";
import accountsProviderGo from "../../../server/internal/staff/accounts_provider.go?raw";
import accountsWorkerGo from "../../../server/internal/staff/accounts_worker.go?raw";
import eventsGo from "../../../server/internal/staff/events.go?raw";
import retentionGo from "../../../server/internal/staff/retention.go?raw";
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
      "absence_visibility_change",
      "percent_not_allowed",
      "hours_on_day_type",
      "carry_over_recorded",
      "own_request",
      "self_certification_exhausted",
      "self_certification_too_early",
      "self_certification_withdrawn",
      "holiday_postponement_not_applicable",
      "holiday_postponement_too_late",
      "seed_immutable",
      "absence_type_in_use",
      "holiday_scheme_in_use",
      "structure_default",
      "revision_conflict",
      "person_erased",
    ];
    for (const code of codes) {
      expect(staffYaml, `the contract names ${code}`).toContain(code);
      for (const [language, catalog] of Object.entries(staffCatalog))
        expect((catalog as Record<string, string>)[`refusal.${code}`], `${language} refusal.${code}`).toBeDefined();
    }
  });

  it("words every reason the accounts worker records on a link, in both languages", () => {
    const reasons = [
      ...`${accountsDecideGo}\n${accountsWorkerGo}\n${accountsProviderGo}`.matchAll(
        /^\s*(?:const\s+)?reason\w+\s*=\s*"(\w+)"/gm,
      ),
    ].map((m) => m[1]);
    // The decision table's twelve and the calls' seven (accounts_decide.go, accounts_worker.go).
    expect(reasons.length).toBeGreaterThanOrEqual(19);
    for (const reason of reasons)
      for (const [language, catalog] of Object.entries(staffCatalog))
        expect(
          (catalog as Record<string, string>)[`accountReason_${reason}`],
          `${language} accountReason_${reason}`,
        ).toBeDefined();
  });

  it("words every step and every failure code an account event carries, in both languages", () => {
    const steps = [...accountsDecideGo.matchAll(/^\s*accountStep\w+\s+accountStepKind\s*=\s*"(\w+)"/gm)].map(
      (m) => m[1],
    );
    expect(steps.sort()).toEqual(["activate", "deactivate", "none", "prepare", "report", "update"]);
    const errors = [...accountsWorkerGo.matchAll(/^\s*error\w+\s*=\s*"(\w+)"/gm)].map((m) => m[1]);
    expect(errors).toEqual(["accounts_unavailable"]);
    for (const [language, catalog] of Object.entries(staffCatalog)) {
      for (const step of steps)
        expect((catalog as Record<string, string>)[`accountStep_${step}`], `${language} ${step}`).toBeDefined();
      for (const code of errors)
        expect((catalog as Record<string, string>)[`accountReason_${code}`], `${language} ${code}`).toBeDefined();
    }
  });

  it("words every state of an account's link and its invitation, in both languages", () => {
    const states = [
      ...`${accountsDecideGo}\n${accountFlagGo}`.matchAll(/^\s*(?:const\s+)?linkState\w+\s*=\s*"(\w+)"/gm),
    ].map((m) => m[1]);
    expect(states.sort()).toEqual(["active", "blocked", "disabled", "failed", "linked", "pending", "preparing"]);
    const invitations = ["none", "active", "expired", "revoked_by_owner", "exhausted"];
    for (const state of invitations) expect(staffYaml, `the contract names ${state}`).toContain(state);
    for (const [language, catalog] of Object.entries(staffCatalog)) {
      for (const state of states)
        expect((catalog as Record<string, string>)[`accountLinkState_${state}`], `${language} ${state}`).toBeDefined();
      for (const state of invitations)
        expect((catalog as Record<string, string>)[`accountInvitation_${state}`], `${language} ${state}`).toBeDefined();
      for (const kind of ["local", "external", "entra", "unavailable"])
        expect((catalog as Record<string, string>)[`accountKind_${kind}`], `${language} ${kind}`).toBeDefined();
      for (const kind of ["local", "external", "entra"])
        expect((catalog as Record<string, string>)[`accountKindChoice_${kind}`], `${language} ${kind}`).toBeDefined();
    }
  });

  it("words every state, reason and reported code of a provider link, in both languages", () => {
    const states = [...accountsProviderGo.matchAll(/^\s*providerState\w+\s*=\s*"(\w+)"/gm)].map((m) => m[1]);
    expect(states.sort()).toEqual(["blocked", "deleted", "disabled", "enabled", "failed", "preparing"]);
    const reported = [...identityProvisionerGo.matchAll(/^\s*Reported\w+\s*=\s*"(\w+)"/gm)].map((m) => m[1]);
    expect(reported.length).toBeGreaterThanOrEqual(5);
    const staffCodes = [...accountsProviderGo.matchAll(/^\s*(?:const\s+)?codeManager\w+\s*=\s*"(\w+)"/gm)].map(
      (m) => m[1],
    );
    expect(staffCodes).toEqual(["manager_not_in_entra"]);
    const steps = [
      "none",
      "prepare",
      "announce",
      "activate",
      "deactivate",
      "update",
      "delete",
      "unexpect",
      "report",
      "provider",
    ];
    for (const [language, catalog] of Object.entries(staffCatalog)) {
      const words = catalog as Record<string, string>;
      for (const state of states) {
        expect(words[`providerLinkState_${state}`], `${language} providerLinkState_${state}`).toBeDefined();
        expect(words[`accountLinkState_${state}`], `${language} accountLinkState_${state}`).toBeDefined();
      }
      for (const code of [...reported, ...staffCodes, "user_gone", "invalid_actor"])
        expect(words[`accountReason_${code}`], `${language} accountReason_${code}`).toBeDefined();
      for (const step of steps) expect(words[`accountStep_${step}`], `${language} accountStep_${step}`).toBeDefined();
      for (const status of ["unconfigured", "connected", "failing"])
        expect(words[`providerStatus_${status}`], `${language} providerStatus_${status}`).toBeDefined();
    }
  });

  it("names every field of the directory profile the contract lists, in both languages", () => {
    const keys = [
      "givenName",
      "familyName",
      "displayName",
      "employeeId",
      "userPrincipalName",
      "usageLocation",
      "employeeHireDate",
      "employeeLeaveDateTime",
      "employeeType",
      "jobTitle",
      "department",
      "companyName",
      "employeeOrgData",
      "officeLocation",
      "workAddress",
      "phones",
      "preferredLanguage",
      "manager",
    ];
    for (const key of keys.slice(6)) expect(staffYaml, `the contract names ${key}`).toContain(key);
    for (const [language, catalog] of Object.entries(staffCatalog))
      for (const key of keys)
        expect((catalog as Record<string, string>)[`directoryField_${key}`], `${language} ${key}`).toBeDefined();
  });

  it("words every refusal of the accounts the contract names, in both languages", () => {
    const codes = [
      "accounts_busy",
      "accounts_unavailable",
      "link_managed",
      "account_not_managed",
      "account_privileged",
      "account_mismatch",
      "account_kind_locked",
      "provider_missing",
      "pass_gone",
      "provider_refused",
    ];
    for (const code of codes) {
      expect(staffYaml, `the contract names ${code}`).toContain(code);
      for (const [language, catalog] of Object.entries(staffCatalog))
        expect((catalog as Record<string, string>)[`refusal.${code}`], `${language} refusal.${code}`).toBeDefined();
    }
  });

  it("knows every kind of event the server writes, and no other", () => {
    const written = [
      ...`${eventsGo}\n${accountFlagGo}\n${accountsWorkerGo}\n${accountsProviderGo}`.matchAll(
        /^\s*event\w+\s*=\s*"(\w+)"/gm,
      ),
    ]
      .map((m) => m[1])
      .sort();
    expect(written.length).toBeGreaterThan(0);
    expect([...eventKinds].sort()).toEqual(written);
  });

  it("names every kind of erase the server reports and the contract lists, in both languages", () => {
    const reported = [...retentionGo.matchAll(/^\s*eraseKind\w+\s*=\s*"(\w+)"/gm)].map((m) => m[1]);
    const schema = staffYaml.slice(staffYaml.indexOf("        StaffErasedKind:"));
    const enumBlock = schema.slice(schema.indexOf("enum:"), schema.indexOf("type:", schema.indexOf("enum:")));
    const listed = [...enumBlock.matchAll(/^\s*-\s*(\w+)\s*$/gm)].map((m) => m[1]);
    expect(reported.length).toBeGreaterThan(0);
    expect([...listed].sort()).toEqual([...reported].sort());
    for (const kind of reported)
      for (const [language, catalog] of Object.entries(staffCatalog))
        expect(
          (catalog as Record<string, string>)[`erasedKind_${kind}`],
          `${language} erasedKind_${kind}`,
        ).toBeDefined();
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
