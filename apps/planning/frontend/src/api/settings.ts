import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type PlanningSettings = Schemas["PlanningSettings"];
export type PlanningSettingsInput = Schemas["PlanningSettingsRequest"];
export type PlanningOverbooking = Schemas["PlanningOverbooking"];

/** The installation's Planning settings (S20), `planning:manage`'s. */
export const planningSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<PlanningSettings>("/api/v1/planning/settings", { signal }),
  });

/** Every field left out keeps its value; the revision read goes back as `If-Match`. */
export const updatePlanningSettings = (input: PlanningSettingsInput, revision: number): Promise<PlanningSettings> =>
  request<PlanningSettings>("/api/v1/planning/settings", json("PUT", input, revision));
