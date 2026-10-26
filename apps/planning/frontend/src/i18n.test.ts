import { describe, expect, it } from "vitest";
import planningYaml from "../../../../openapi/planning.yaml?raw";
import { allocationStatuses, warningCodes } from "./api/allocations";
import { competenceKinds, competenceLevels } from "./api/competences";
import { eventEntityKinds, eventKinds } from "./api/events";
import { coverages, intakeFlags } from "./api/intake";
import { projectStatuses } from "./api/projects";
import { candidateAvailabilities, candidateFlagCodes, preferenceKinds, requestStatuses } from "./api/requests";
import { attentionTypes } from "./api/stats";
import { timelineFlagCodes } from "./api/timeline";
import { planningCatalog } from "./i18n";
import { planningNavItems } from "./lib/navigation";

const placeholders = (text: string): string[] => [...text.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]).sort();

/** The values of the contract's enum for a schema — or for one property of it. */
const enumOf = (schema: string, property?: string): string[] => {
  let from = planningYaml.indexOf(`        ${schema}:\n`);
  expect(from, `the contract has ${schema}`).toBeGreaterThan(-1);
  if (property) from = planningYaml.indexOf(`${property}:`, from);
  const rest = planningYaml.slice(planningYaml.indexOf("enum:", from) + "enum:".length);
  const values: string[] = [];
  for (const line of rest.split("\n").slice(1)) {
    const value = /^\s+- (.+)$/.exec(line);
    if (!value) break;
    values.push(value[1].replace(/^['"]|['"]$/g, ""));
  }
  return values;
};

/** The refusal codes the module answers (§4.7 and amendments 7–8; delivery B's request_not_open). */
const refusalCodes = [
  "overbooked",
  "outside_employment",
  "project_closed",
  "request_not_open",
  "period_too_long",
  "revision_conflict",
  "no_person",
  "dependency_unavailable",
  "window_out_of_bounds",
];

describe("the planning catalog", () => {
  it("has every key in both languages, and no other", () => {
    expect(Object.keys(planningCatalog.nb).sort()).toEqual(Object.keys(planningCatalog.en).sort());
  });

  it("says something for every key in both languages", () => {
    for (const [language, catalog] of Object.entries(planningCatalog)) {
      for (const [key, text] of Object.entries(catalog)) {
        expect(String(text).trim(), `${language}.${key} is empty`).not.toBe("");
      }
    }
  });

  it("interpolates the same values in both languages", () => {
    const en = planningCatalog.en as Record<string, string>;
    const nb = planningCatalog.nb as Record<string, string>;
    for (const key of Object.keys(en)) {
      expect(placeholders(nb[key] ?? ""), `nb.${key}`).toEqual(placeholders(en[key]));
    }
  });

  it("names every status, unit, warning, refusal, flag, attention type and sidebar destination", () => {
    const keys = [
      ...allocationStatuses.map((value) => `status_${value}`),
      ...allocationStatuses.map((value) => `statusDescription_${value}`),
      ...["percent", "hours_per_week"].map((value) => `loadUnit_${value}`),
      ...projectStatuses.map((value) => `projectStatus_${value}`),
      ...warningCodes.map((value) => `warning_${value}`),
      ...refusalCodes.map((value) => `refusal.${value}`),
      ...timelineFlagCodes.map((value) => `flag_${value}`),
      ...attentionTypes.map((value) => `attention_${value}`),
      ...["warn", "refuse"].map((value) => `overbooking_${value}`),
      ...requestStatuses.map((value) => `requestStatus_${value}`),
      ...candidateAvailabilities.map((value) => `availability_${value}`),
      ...candidateFlagCodes.map((value) => `candidateFlag_${value}`),
      ...preferenceKinds.map((value) => `preferenceKind_${value}`),
      ...coverages.map((value) => `coverage_${value}`),
      ...intakeFlags.map((value) => `intakeFlag_${value}`),
      ...competenceKinds.map((value) => `competenceKind_${value}`),
      ...competenceLevels.map((value) => `level_${value}`),
      ...planningNavItems.map((item) => item.label),
    ];
    for (const [language, catalog] of Object.entries(planningCatalog))
      for (const key of keys)
        expect((catalog as Record<string, string>)[key], `${language}: no label for ${key}`).toBeDefined();
  });

  it("keeps the enumerations in step with the contract", () => {
    expect([...allocationStatuses].sort()).toEqual(enumOf("PlanningAllocationStatus").sort());
    expect([...projectStatuses].sort()).toEqual(enumOf("PlanningProjectStatus").sort());
    expect(["hours_per_week", "percent"]).toEqual(enumOf("PlanningLoadUnit").sort());
    expect(["refuse", "warn"]).toEqual(enumOf("PlanningOverbooking").sort());
    expect([...warningCodes].sort()).toEqual(enumOf("PlanningWarning", "code").sort());
    expect([...timelineFlagCodes].sort()).toEqual(enumOf("PlanningTimelineFlag", "code").sort());
    expect([...attentionTypes].sort()).toEqual(enumOf("PlanningStatsAttentionItem", "type").sort());
    expect([...eventKinds].sort()).toEqual(enumOf("PlanningEvent", "kind").sort());
    expect([...eventEntityKinds].sort()).toEqual(enumOf("PlanningEvent", "entityKind").sort());
    expect([...requestStatuses].sort()).toEqual(enumOf("PlanningRequestStatus").sort());
    expect([...candidateAvailabilities].sort()).toEqual(enumOf("PlanningCandidate", "availability").sort());
    expect([...candidateFlagCodes].sort()).toEqual(enumOf("PlanningCandidateFlag", "code").sort());
    expect([...coverages].sort()).toEqual(enumOf("PlanningCoverage").sort());
    expect([...intakeFlags].sort()).toEqual(enumOf("PlanningIntakeRequest", "flags").sort());
    expect([...competenceKinds].sort()).toEqual(enumOf("PlanningCompetence", "kind").sort());
  });

  it("words every refusal the contract names", () => {
    for (const code of refusalCodes) expect(planningYaml, `the contract names ${code}`).toContain(code);
  });
});
