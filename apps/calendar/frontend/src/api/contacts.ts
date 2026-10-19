import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, request } from "./request";

/** A customer's contact with an address, as the attendee picker offers them. */
export type CalendarContact = components["schemas"]["CalendarContact"];

/**
 * The contacts of one customer that have an address (`GET /contacts`, design
 * D2), ordered by name. A caller without Customers' contact keys is answered
 * 403, and the sheet then offers no contacts at all — so no retry.
 */
export const calendarContactsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "contacts", customerId],
    queryFn: ({ signal }) =>
      request<CalendarContact[]>(
        `/api/v1/calendar/contacts?${new URLSearchParams({ customerId: String(customerId) })}`,
        {
          signal,
        },
      ),
    retry: false,
    staleTime: 60 * 1000,
  });
