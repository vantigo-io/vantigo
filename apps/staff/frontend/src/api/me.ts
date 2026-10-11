import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { StaffEvent, StaffPerson } from "./people";
import { json, type Paginated, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

export type StaffMeInput = Schemas["StaffMeRequest"];

/**
 * My page's person: the caller's own record through their self-service link,
 * read as themselves. A caller linked to nobody gets `no_person` (404) — the
 * one 404 this path answers, since it has no id to be unknown.
 */
export const meQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me"],
    queryFn: ({ signal }) => request<StaffPerson>("/api/v1/staff/me", { signal }),
    retry: false,
  });

/** What a person edits themselves (design S17): the private phones and email, the address, the next of kin. */
export const updateMe = (input: StaffMeInput, revision: number): Promise<StaffPerson> =>
  request<StaffPerson>("/api/v1/staff/me", json("PUT", input, revision));

export const myEventsQueryOptions = (page: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "events", page],
    queryFn: ({ signal }) => request<Paginated<StaffEvent>>(`/api/v1/staff/me/events?page=${page}`, { signal }),
    placeholderData: keepPreviousData,
  });
