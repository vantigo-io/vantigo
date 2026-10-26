import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type PlanningEvent = Schemas["PlanningEvent"];
export type PlanningEventKind = PlanningEvent["kind"];

export const eventKinds: readonly PlanningEventKind[] = [
  "allocation_created",
  "allocation_changed",
  "allocation_confirmed",
  "allocation_unconfirmed",
  "allocation_deleted",
];

export interface PlanningEventsFilter {
  personId?: number;
  projectId?: number;
  allocationId?: number;
}

/** The record of writes (S23), newest first (for delivery B: the events screen). */
export const planningEventsQueryOptions = (filter: PlanningEventsFilter = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "events", filter],
    queryFn: ({ signal }) =>
      request<Schemas["PlanningEventList"]>(`/api/v1/planning/events${queryString({ ...filter })}`, { signal }).then(
        (answer) => answer.events,
      ),
  });
