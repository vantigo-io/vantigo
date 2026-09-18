import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { TimeEntry } from "./entries";
import { request } from "./request";

type Schemas = components["schemas"];

export type TimeWeekDay = Omit<Schemas["TimeWeekDay"], "entries"> & { entries: TimeEntry[] };

/** One trackable — project, optional line, optional task — across the week's seven days. */
export type TimeWeekRow = Omit<Schemas["TimeWeekRow"], "days"> & { days: TimeWeekDay[] };

export type TimeWeekTotals = Schemas["TimeWeekTotals"];

/**
 * The caller's own week: a row per trackable they logged time on, each with
 * seven days Monday first, and the totals per day and for the week.
 * `hasUnsubmittedChanges` is true when the week was submitted and a draft has
 * appeared in it since.
 */
export type TimeWeek = Omit<Schemas["TimeWeekResponse"], "rows"> & { rows: TimeWeekRow[] };

export const weekQueryOptions = (weekStart: string) =>
  queryOptions({
    queryKey: ["time", "weeks", weekStart],
    queryFn: ({ signal }) => request<TimeWeek>(`/api/v1/time/weeks/${weekStart}`, { signal }),
  });

/** Submits every draft of the caller's in the week. An empty week may be submitted too. */
export const submitWeek = (weekStart: string): Promise<TimeWeek> =>
  request<TimeWeek>(`/api/v1/time/weeks/${weekStart}/submit`, { method: "POST" });
