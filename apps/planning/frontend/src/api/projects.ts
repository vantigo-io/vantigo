import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type PlanningProject = Schemas["PlanningProject"];
export type PlanningProjectRef = Schemas["PlanningProjectRef"];
export type PlanningProjectStatus = Schemas["PlanningProjectStatus"];

/** The project statuses as Projects answers them, in its order. */
export const projectStatuses: readonly PlanningProjectStatus[] = [
  "planned",
  "active",
  "on-hold",
  "completed",
  "cancelled",
];

export interface PlanningProjectsFilter {
  /** Statuses; left out, the server's plannable ones (planned, active, on hold). */
  status?: readonly PlanningProjectStatus[];
  /** Matches the code or the name. */
  q?: string;
}

/** The plannable projects by code (S9), for the pickers and the board's project filter. */
export const planningProjectsQueryOptions = (filter: PlanningProjectsFilter = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "projects", filter.status?.join(",") ?? "", filter.q ?? ""],
    queryFn: ({ signal }) =>
      request<Schemas["PlanningProjectList"]>(
        `/api/v1/planning/projects${queryString({ status: filter.status?.join(","), q: filter.q })}`,
        { signal },
      ).then((answer) => answer.projects),
  });
