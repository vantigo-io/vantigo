import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, PLANNING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type PlanningAllocation = Schemas["PlanningAllocation"];
export type PlanningAllocationInput = Schemas["PlanningAllocationInput"];
export type PlanningAllocationStatus = Schemas["PlanningAllocationStatus"];
export type PlanningAllocationWritten = Schemas["PlanningAllocationWritten"];
export type PlanningWarning = Schemas["PlanningWarning"];
export type PlanningWarningCode = PlanningWarning["code"];

export const allocationStatuses: readonly PlanningAllocationStatus[] = ["tentative", "confirmed"];

/** What a write may warn of (rule 6; delivery C's non-compete on a tentative row, S17.3). */
export const warningCodes: readonly PlanningWarningCode[] = [
  "overbooked",
  "booked_while_away",
  "outside_project_dates",
  "non_compete",
];

export interface PlanningAllocationsFilter {
  personId?: number;
  projectId?: number;
  from?: string;
  to?: string;
  status?: PlanningAllocationStatus;
}

/** At most 500 rows. */
export const allocationsQueryOptions = (filter: PlanningAllocationsFilter = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "allocations", filter],
    queryFn: ({ signal }) =>
      request<Schemas["PlanningAllocationList"]>(`/api/v1/planning/allocations${queryString({ ...filter })}`, {
        signal,
      }).then((answer) => answer.allocations),
  });

export const allocationQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "allocations", id],
    queryFn: ({ signal }) => request<PlanningAllocation>(`/api/v1/planning/allocations/${id}`, { signal }),
  });

/** A new row (S1–S7); `status` left out defaults by the project's status. The answer carries the warnings. */
export const createAllocation = (input: PlanningAllocationInput): Promise<PlanningAllocationWritten> =>
  send<PlanningAllocationWritten>("/api/v1/planning/allocations", json("POST", input));

/** A whole replace at the revision the sheet was opened at. */
export const updateAllocation = (
  id: number,
  input: PlanningAllocationInput,
  revision: number,
): Promise<PlanningAllocationWritten> =>
  send<PlanningAllocationWritten>(`/api/v1/planning/allocations/${id}`, json("PUT", input, revision));

export const deleteAllocation = (id: number): Promise<void> =>
  request<void>(`/api/v1/planning/allocations/${id}`, { method: "DELETE" });

/** Confirming is its own operation (S3): the moment the person is told. */
export const confirmAllocation = (id: number): Promise<PlanningAllocationWritten> =>
  request<PlanningAllocationWritten>(`/api/v1/planning/allocations/${id}/confirm`, { method: "POST" });
