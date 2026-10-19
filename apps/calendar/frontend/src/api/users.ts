import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { CALENDAR_QUERY_KEY, request } from "./request";

/** A user as the directory names them. */
export type CalendarUser = components["schemas"]["CalendarUserRef"];

/**
 * Active users from identity's directory, for the attendee and colleague
 * pickers (`GET /users`): at most `limit`, narrowed by the term.
 */
export const calendarUsersQueryOptions = (search: string, limit = 20) => {
  const term = search.trim();
  return queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "users", term, limit],
    queryFn: ({ signal }) => {
      const params = new URLSearchParams({ limit: String(limit) });
      if (term) params.set("query", term);
      return request<CalendarUser[]>(`/api/v1/calendar/users?${params}`, { signal });
    },
    placeholderData: keepPreviousData,
  });
};
