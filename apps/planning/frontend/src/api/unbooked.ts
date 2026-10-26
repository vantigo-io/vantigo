import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type PlanningUnbooked = Schemas["PlanningUnbooked"];
export type PlanningUnbookedPerson = Schemas["PlanningUnbookedPerson"];

/** The people under the threshold within the horizon (S13), in the server's order (amendment 9). */
export const unbookedQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "unbooked"],
    queryFn: ({ signal }) => request<PlanningUnbooked>("/api/v1/planning/unbooked", { signal }),
  });
