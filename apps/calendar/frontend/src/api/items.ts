import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type CalendarItems = Schemas["CalendarItems"];
export type CalendarItem = Schemas["CalendarItem"];
export type CalendarItemAppointment = Schemas["CalendarItemAppointment"];
export type CalendarPublicHoliday = Schemas["CalendarPublicHoliday"];

/** A window of civil days, both inclusive, judged in the viewer's zone (design D5: at most 93 days). */
export interface CalendarWindow {
  from: string;
  to: string;
  timeZone: string;
}

export interface CalendarItemsWindow extends CalendarWindow {
  /** Narrows the providers asked; left out, every kind. */
  kinds?: readonly string[];
}

const windowQuery = ({ from, to, timeZone }: CalendarWindow, kinds?: readonly string[]) => {
  const params = new URLSearchParams({ from, to, timeZone });
  if (kinds && kinds.length > 0) params.set("kinds", kinds.join(","));
  return params.toString();
};

/** The caller's own calendar in a window: their appointments, the providers' items and the holidays. */
export const calendarItemsQueryOptions = ({ from, to, timeZone, kinds }: CalendarItemsWindow) =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "items", { from, to, timeZone, kinds: kinds ? [...kinds] : undefined }],
    queryFn: ({ signal }) =>
      request<CalendarItems>(`/api/v1/calendar/items?${windowQuery({ from, to, timeZone }, kinds)}`, { signal }),
  });

/**
 * A colleague's calendar in a window (design D6): their busy appointments
 * only, a private one with its title blank, and no providers' items.
 */
export const colleagueItemsQueryOptions = (userId: string, { from, to, timeZone }: CalendarWindow) =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "colleagues", userId, "items", { from, to, timeZone }],
    queryFn: ({ signal }) =>
      request<CalendarItems>(
        `/api/v1/calendar/colleagues/${encodeURIComponent(userId)}/items?${windowQuery({ from, to, timeZone })}`,
        { signal },
      ),
  });
