import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request, STAFF_QUERY_KEY } from "./request";

export type StaffStatsAttentionItem = components["schemas"]["StaffStatsAttentionItem"];
export type StaffAttentionType = StaffStatsAttentionItem["type"];

/**
 * The caller's attention items, as the dashboard reads them: staff:manage's
 * for everyone, a manager's for the people they manage today — an empty list,
 * never a 403, for anyone else. My team filters them by type.
 */
export const attentionQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "stats", "attention"],
    queryFn: ({ signal }) => request<StaffStatsAttentionItem[]>("/api/v1/staff/stats/attention", { signal }),
  });
