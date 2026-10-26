import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type PlanningStats = Schemas["PlanningStats"];
export type PlanningAttentionItem = Schemas["PlanningStatsAttentionItem"];
export type PlanningAttentionType = PlanningAttentionItem["type"];

/** The attention items (S22): delivery A's four, B's open requests and C's unseen wishes. */
export const attentionTypes: readonly PlanningAttentionType[] = [
  "unbooked",
  "overbooked",
  "endingWithoutFollowOn",
  "certificatesExpiring",
  "requestsOpen",
  "wishesUnseen",
];

/** The dashboard card's figures (S22). */
export const planningStatsQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "stats"],
    queryFn: ({ signal }) => request<PlanningStats>("/api/v1/planning/stats", { signal }),
  });

/** What the dashboard asks a human to look at, shaped by the caller's audience. */
export const planningAttentionQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "stats", "attention"],
    queryFn: ({ signal }) => request<PlanningAttentionItem[]>("/api/v1/planning/stats/attention", { signal }),
  });
