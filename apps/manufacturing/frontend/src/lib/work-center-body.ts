import type {
  ManufacturingWeekPattern,
  ManufacturingWorkCenter,
  ManufacturingWorkCenterInput,
  ManufacturingWorkCenterUpdate,
} from "../api/work-centers";
import { isDecimal, normalizeDecimal } from "./decimal";
import { defaultPattern } from "./pattern";

/** A work center's form (M8), as the person fills it in. Decimals stay text. */
export interface WorkCenterValues {
  code: string;
  name: string;
  siteId: number | null;
  capacity: number | "";
  timeEfficiency: number | "";
  setupMinutes: string;
  cleanupMinutes: string;
  machineCostPerHour: string;
  labourCostPerHour: string;
  pattern: ManufacturingWeekPattern;
}

/** A code as the server keeps it: 1–16 characters A–Z and 0–9, upper-cased (M8). */
export const WORK_CENTER_CODE_PATTERN = /^[A-Z0-9]{1,16}$/;

export const workCenterValuesOf = (center?: ManufacturingWorkCenter): WorkCenterValues => ({
  code: center?.code ?? "",
  name: center?.name ?? "",
  siteId: center?.siteId ?? null,
  capacity: center?.capacity ?? 1,
  timeEfficiency: center?.timeEfficiency ?? 100,
  setupMinutes: center?.setupMinutes ?? "0",
  cleanupMinutes: center?.cleanupMinutes ?? "0",
  machineCostPerHour: center?.machineCostPerHour ?? "",
  labourCostPerHour: center?.labourCostPerHour ?? "",
  pattern: center?.pattern ?? defaultPattern(),
});

const rate = (text: string) => (text.trim() === "" ? null : normalizeDecimal(text));

/**
 * The request a work center's form sends. The cost rates are sent only by a
 * caller who may see them (`canSeeCosts`): the server refuses a rate from
 * anyone else (`costs_view_required`), and an absent rate keeps the stored
 * one on an update, so without the permission they are left out entirely.
 * An empty rate clears it (null).
 */
export const workCenterInputOf = (values: WorkCenterValues, canSeeCosts: boolean): ManufacturingWorkCenterInput => ({
  code: values.code.trim().toUpperCase(),
  name: values.name.trim(),
  siteId: values.siteId ?? 0,
  capacity: values.capacity === "" ? 1 : values.capacity,
  timeEfficiency: values.timeEfficiency === "" ? 100 : values.timeEfficiency,
  setupMinutes: values.setupMinutes.trim() === "" ? "0" : normalizeDecimal(values.setupMinutes),
  cleanupMinutes: values.cleanupMinutes.trim() === "" ? "0" : normalizeDecimal(values.cleanupMinutes),
  ...(canSeeCosts
    ? { machineCostPerHour: rate(values.machineCostPerHour), labourCostPerHour: rate(values.labourCostPerHour) }
    : {}),
  pattern: values.pattern,
});

export const workCenterUpdateOf = (
  values: WorkCenterValues,
  canSeeCosts: boolean,
  revision: number,
): ManufacturingWorkCenterUpdate => ({ ...workCenterInputOf(values, canSeeCosts), revision });

/** What the form can tell before it sends, by field; the pattern is judged by `patternIssues`. */
export const workCenterProblems = (values: WorkCenterValues, canSeeCosts: boolean): Record<string, string> => {
  const problems: Record<string, string> = {};
  if (!WORK_CENTER_CODE_PATTERN.test(values.code.trim().toUpperCase())) problems.code = "codeHint";
  if (values.name.trim() === "" || values.name.trim().length > 200) problems.name = "required";
  if (values.siteId === null) problems.siteId = "required";
  if (values.capacity !== "" && (!Number.isInteger(values.capacity) || values.capacity < 1))
    problems.capacity = "capacityHint";
  if (
    values.timeEfficiency !== "" &&
    (!Number.isInteger(values.timeEfficiency) || values.timeEfficiency < 1 || values.timeEfficiency > 500)
  )
    problems.timeEfficiency = "timeEfficiencyHint";
  for (const field of ["setupMinutes", "cleanupMinutes"] as const)
    if (values[field].trim() !== "" && !isDecimal(values[field], 99)) problems[field] = "notNegative";
  if (canSeeCosts)
    for (const field of ["machineCostPerHour", "labourCostPerHour"] as const)
      if (values[field].trim() !== "" && !isDecimal(values[field], 99)) problems[field] = "notNegative";
  return problems;
};
