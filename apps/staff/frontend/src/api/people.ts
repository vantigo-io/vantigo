import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, type Paginated, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

export type StaffPerson = Schemas["StaffPerson"];
export type StaffPersonInput = Schemas["StaffPersonRequest"];
export type StaffPersonListItem = Schemas["StaffPersonListItem"];
export type StaffPersonStatus = Schemas["StaffPersonStatus"];
export type StaffPersonSummary = Schemas["StaffPersonSummary"];

/** `staff:manage`'s filter on where people stand today; anyone else lists the people active today. */
export type PeopleStatusFilter = "current" | "ended" | "all";

export interface PeopleFilters {
  q?: string;
  unitId?: number;
  departmentId?: number;
  locationId?: number;
  status?: PeopleStatusFilter;
  page?: number;
  pageSize?: number;
}

const listQuery = (filters: PeopleFilters): string => {
  const query = new URLSearchParams();
  if (filters.q) query.set("q", filters.q);
  if (filters.unitId !== undefined) query.set("unitId", String(filters.unitId));
  if (filters.departmentId !== undefined) query.set("departmentId", String(filters.departmentId));
  if (filters.locationId !== undefined) query.set("locationId", String(filters.locationId));
  if (filters.status) query.set("status", filters.status);
  if (filters.page !== undefined) query.set("page", String(filters.page));
  if (filters.pageSize !== undefined) query.set("pageSize", String(filters.pageSize));
  const search = query.toString();
  return search ? `?${search}` : "";
};

/** One page of the directory, by display name, with the work group only. */
export const peopleQueryOptions = (filters: PeopleFilters) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "list", filters],
    queryFn: ({ signal }) =>
      request<Paginated<StaffPersonListItem>>(`/api/v1/staff/people${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

/** One person, shaped by who is asking; an outsider gets the unknown id's 404. */
export const personQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id],
    queryFn: ({ signal }) => request<StaffPerson>(`/api/v1/staff/people/${id}`, { signal }),
  });

/**
 * The first step of New person. Left out, the employee number is generated
 * from the settings' prefix and next number, and the display name from the
 * two names.
 */
export const createPerson = (input: StaffPersonInput): Promise<StaffPerson> =>
  request<StaffPerson>("/api/v1/staff/people", json("POST", input));
