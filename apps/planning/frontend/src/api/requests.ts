import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, PLANNING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type PlanningRequest = Schemas["PlanningRequest"];
export type PlanningRequestInput = Schemas["PlanningRequestInput"];
export type PlanningRequestStatus = Schemas["PlanningRequestStatus"];
export type PlanningRequestWritten = Schemas["PlanningRequestWritten"];
export type PlanningRequestFilled = Schemas["PlanningRequestFilled"];
export type PlanningRequirement = Schemas["PlanningRequirement"];
export type PlanningRequirementInput = Schemas["PlanningRequirementInput"];
export type PlanningFillInput = Schemas["PlanningFillInput"];
export type PlanningCandidate = Schemas["PlanningCandidate"];
export type PlanningCandidateList = Schemas["PlanningCandidateList"];
export type PlanningCandidateFlag = Schemas["PlanningCandidateFlag"];
export type PlanningCandidateFlagCode = PlanningCandidateFlag["code"];
export type PlanningCandidateAvailability = PlanningCandidate["availability"];

/** A request's statuses (S16): open is demand, filled while a row names it, withdrawn for good. */
export const requestStatuses: readonly PlanningRequestStatus[] = ["open", "filled", "withdrawn"];

/** What a candidate may be flagged with (S17; delivery B). */
export const candidateFlagCodes: readonly PlanningCandidateFlagCode[] = ["certificate_expires"];

export const candidateAvailabilities: readonly PlanningCandidateAvailability[] = ["fits", "partial"];

/**
 * The preference kinds Staff may say a booking lowers a candidate's fit by
 * (S17.6), in its catalogue's order; the contract answers them as strings, so
 * a kind added later is shown by its key until it is named here.
 */
export const preferenceKinds = [
  "weekdays",
  "time_of_day",
  "location",
  "travel",
  "remote",
  "max_consecutive_days",
  "max_hours_per_week",
] as const;

/** The most requirements a request takes (S16). */
export const MAX_REQUIREMENTS = 10;

export interface PlanningRequestsFilter {
  projectId?: number;
  status?: PlanningRequestStatus;
  /** A window: the requests touching it. Left out, every request. */
  from?: string;
  to?: string;
}

/** The most requests the list answers (S16): a full page means some were left out. */
export const REQUESTS_LIMIT = 500;

/** At most `REQUESTS_LIMIT` requests, by from date then id. */
export const requestsQueryOptions = (filter: PlanningRequestsFilter = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "requests", filter],
    queryFn: ({ signal }) =>
      request<Schemas["PlanningRequestList"]>(`/api/v1/planning/requests${queryString({ ...filter })}`, {
        signal,
      }).then((answer) => answer.requests),
  });

export const requestQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "requests", id],
    queryFn: ({ signal }) => request<PlanningRequest>(`/api/v1/planning/requests/${id}`, { signal }),
  });

/**
 * The people who could fill a request (S17), sorted by the server; with
 * `includePartial` also those who miss a requirement. `planning:manage`'s.
 */
export const candidatesQueryOptions = (id: number, includePartial = false) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "requests", id, "candidates", { includePartial }],
    queryFn: ({ signal }) =>
      request<PlanningCandidateList>(
        `/api/v1/planning/requests/${id}/candidates${queryString({ includePartial: includePartial || undefined })}`,
        { signal },
      ),
  });

/** A new request on a project (S16). The answer carries the warnings (outside_project_dates). */
export const createRequest = (input: PlanningRequestInput): Promise<PlanningRequestWritten> =>
  send<PlanningRequestWritten>("/api/v1/planning/requests", json("POST", input));

/** A whole replace of an open request at the revision the sheet was opened at. */
export const updateRequest = (
  id: number,
  input: PlanningRequestInput,
  revision: number,
): Promise<PlanningRequestWritten> =>
  send<PlanningRequestWritten>(`/api/v1/planning/requests/${id}`, json("PUT", input, revision));

/** Closes an open request for good; a withdrawn request is never reopened. */
export const withdrawRequest = (id: number): Promise<PlanningRequest> =>
  send<PlanningRequest>(`/api/v1/planning/requests/${id}/withdraw`, { method: "POST" });

/**
 * Fills an open request with a person: the allocation is created from the
 * request under the allocation rules, and the answer carries its warnings.
 */
export const fillRequest = (id: number, input: PlanningFillInput): Promise<PlanningRequestFilled> =>
  send<PlanningRequestFilled>(`/api/v1/planning/requests/${id}/fill`, json("POST", input));
