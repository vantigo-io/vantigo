import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type CalendarMeta = Schemas["CalendarMeta"];
export type CalendarSourceKind = Schemas["CalendarSourceKind"];

/**
 * What the app needs before it asks for a window: the business zone and its
 * today, whether the caller may look at a colleague's calendar, and every
 * kind the enabled modules project — the legend's chips.
 */
export const calendarMetaQueryOptions = () =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<CalendarMeta>("/api/v1/calendar/meta", { signal }),
    staleTime: 5 * 60 * 1000,
  });
