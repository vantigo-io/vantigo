import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

export type TimePersonOverview = Schemas["TimePersonOverview"];
export type TimePersonWeek = Schemas["TimePersonWeek"];

/** How many weeks back the overview reads when nothing else is asked for. */
export const PEOPLE_DEFAULT_WEEKS = 4;

/** Everyone's hours over the last weeks, newest week last. `time:view-all` only. */
export const peopleOverviewQueryOptions = (weeks: number = PEOPLE_DEFAULT_WEEKS) =>
  queryOptions({
    queryKey: ["time", "people", weeks],
    queryFn: ({ signal }) =>
      request<TimePersonOverview[]>(`/api/v1/time/people?${new URLSearchParams({ weeks: String(weeks) })}`, {
        signal,
      }),
  });
