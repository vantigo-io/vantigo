import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * What the app may do, answered by the server rather than re-derived here
 * (design §5): `canView`, `canManage`, whether Staff answers a person for the
 * caller, whether Staff answers anybody at all (`workforceVisible` — a
 * planner without `staff:view` sees an explanation, never an empty plan), the
 * settings for `manage`, and the load units.
 */
export type PlanningMeta = Schemas["PlanningMeta"];
export type PlanningLoadUnit = Schemas["PlanningLoadUnit"];

export const planningMetaQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<PlanningMeta>("/api/v1/planning/meta", { signal }),
    staleTime: 60_000,
  });
