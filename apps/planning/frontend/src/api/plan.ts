import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type PlanningProjectPlan = Schemas["PlanningProjectPlan"];
export type PlanningProjectPlanWeek = Schemas["PlanningProjectPlanWeek"];

/** A project's plan over a window (S11): the project, its allocations, its booked and tentative hours a week. */
export const projectPlanQueryOptions = (projectId: number, window: { from?: string; to?: string } = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "plan", projectId, window],
    queryFn: ({ signal }) =>
      request<PlanningProjectPlan>(`/api/v1/planning/projects/${projectId}/plan${queryString({ ...window })}`, {
        signal,
      }),
  });
