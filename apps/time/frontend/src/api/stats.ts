import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

export type TimeStatsSummary = Schemas["TimeStatsSummaryResponse"];
export type TimeStatsDailyBucket = Schemas["TimeStatsDailyBucket"];
export type TimeStatsAttentionItem = Omit<Schemas["TimeStatsAttentionItem"], "type"> & {
  type: "weekUnsubmitted" | "approvalWaiting";
};
export type TimeProjectSummary = Schemas["TimeProjectSummaryResponse"];

/** The metrics the dashboard's sparkline may ask for. */
export type TimeStatsMetric = "hours" | "billableHours";

/** A period as RFC 3339 instants; either end left out takes the server's default (the last 30 days). */
export interface TimeStatsPeriod {
  from?: string;
  to?: string;
}

const periodQuery = (period: TimeStatsPeriod, extra: Record<string, string> = {}): string => {
  const query = new URLSearchParams(extra);
  if (period.from) query.set("from", period.from);
  if (period.to) query.set("to", period.to);
  const search = query.toString();
  return search ? `?${search}` : "";
};

/** The caller's hours this week and what waits for their approval, each with its change. */
export const timeStatsSummaryQueryOptions = (period: TimeStatsPeriod = {}) =>
  queryOptions({
    queryKey: ["time", "stats", "summary", period],
    queryFn: ({ signal }) => request<TimeStatsSummary>(`/api/v1/time/stats/summary${periodQuery(period)}`, { signal }),
  });

/** The caller's own hours per day; a day with none is absent. */
export const timeStatsTimeseriesQueryOptions = (metric: TimeStatsMetric, period: TimeStatsPeriod = {}) =>
  queryOptions({
    queryKey: ["time", "stats", "timeseries", metric, period],
    queryFn: ({ signal }) =>
      request<TimeStatsDailyBucket[]>(`/api/v1/time/stats/timeseries${periodQuery(period, { metric })}`, {
        signal,
      }),
  });

export const timeStatsAttentionQueryOptions = () =>
  queryOptions({
    queryKey: ["time", "stats", "attention"],
    queryFn: ({ signal }) => request<TimeStatsAttentionItem[]>("/api/v1/time/stats/attention", { signal }),
  });

/** Hours on one project by status, line and person; `billing` only for its financial viewers. */
export const projectTimeSummaryQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["time", "project-summary", projectId],
    queryFn: ({ signal }) => request<TimeProjectSummary>(`/api/v1/time/projects/${projectId}/summary`, { signal }),
  });
