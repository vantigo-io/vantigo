import { queryOptions } from "@tanstack/react-query";
import { CALENDAR_QUERY_KEY, request } from "./request";

/** The signed-in user's id, which the attendee picker leaves out: an owner is never their own attendee. */
export const sessionUserIdQueryOptions = () =>
  queryOptions({
    queryKey: [CALENDAR_QUERY_KEY, "session-user"],
    queryFn: async ({ signal }) =>
      (await request<{ user?: { id?: string } }>("/api/v1/identity/session", { signal })).user?.id ?? null,
    staleTime: Number.POSITIVE_INFINITY,
  });
