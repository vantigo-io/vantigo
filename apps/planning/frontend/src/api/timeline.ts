import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type PlanningTimeline = Schemas["PlanningTimeline"];
export type PlanningTimelineFlag = Schemas["PlanningTimelineFlag"];
export type PlanningTimelineFlagCode = PlanningTimelineFlag["code"];

/** The flags a timeline marks an allocation with (S12). */
export const timelineFlagCodes: readonly PlanningTimelineFlagCode[] = ["beyond_employment", "certificate_expires"];

/**
 * One person's forward view (S12). A caller holding `planning:access` alone
 * reads their own, with confirmed rows and nothing else (amendment 2).
 */
export const personTimelineQueryOptions = (personId: number, window: { from?: string; to?: string } = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "timeline", personId, window],
    queryFn: ({ signal }) =>
      request<PlanningTimeline>(`/api/v1/planning/people/${personId}/timeline${queryString({ ...window })}`, {
        signal,
      }),
  });
