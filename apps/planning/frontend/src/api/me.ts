import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type PlanningMe = Schemas["PlanningMe"];

/**
 * My planning (S19): the caller's person and their confirmed allocations from
 * today on. A caller Staff answers no person for gets `no_person` (404).
 */
export const myPlanningQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "me"],
    queryFn: ({ signal }) => request<PlanningMe>("/api/v1/planning/me", { signal }),
    retry: false,
  });
