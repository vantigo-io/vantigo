import type { components } from "../api-schema";
import type { PlanningAllocation } from "./allocations";
import { json, send } from "./request";

type Schemas = components["schemas"];

export type PlanningTerms = Schemas["PlanningTerms"];
export type PlanningTermsInput = Schemas["PlanningTermsInput"];
export type PlanningNoticeUnit = Schemas["PlanningNoticeUnit"];
export type PlanningCustomerRef = Schemas["PlanningCustomerRef"];

/** The units a notice period is counted in (S14). */
export const noticeUnits: readonly PlanningNoticeUnit[] = ["days", "weeks", "months"];

/** The most customers a non-compete names (S14). */
export const MAX_NON_COMPETE_CUSTOMERS = 20;

/**
 * Sets an allocation's terms whole (S14): a field left out is cleared. The
 * terms carry their own revision, sent as `If-Match` when the row has terms
 * and left out on a first set (reading 2); either way a mismatch is
 * `revision_conflict`. Answers the allocation with its terms and the derived
 * dates (S15).
 */
export const setAllocationTerms = (
  id: number,
  input: PlanningTermsInput,
  revision?: number,
): Promise<PlanningAllocation> =>
  send<PlanningAllocation>(`/api/v1/planning/allocations/${id}/terms`, json("PUT", input, revision));

/** Clears an allocation's terms; the derived dates fall back to the row's own end. */
export const clearAllocationTerms = (id: number): Promise<void> =>
  send<void>(`/api/v1/planning/allocations/${id}/terms`, { method: "DELETE" });
