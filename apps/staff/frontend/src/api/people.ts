import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { downloadFile, type FileDownload } from "./documents";
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

export type StaffNextOfKin = Schemas["StaffNextOfKin"];
export type StaffNextOfKinList = Schemas["StaffNextOfKinList"];
export type StaffNextOfKinRelationship = Schemas["StaffNextOfKinRelationship"];
export type StaffPersonRole = Schemas["StaffPersonRole"];
export type StaffPersonRoleList = Schemas["StaffPersonRoleList"];
export type StaffPersonRoleKind = Schemas["StaffPersonRoleKind"];
export type StaffPersonLink = Schemas["StaffPersonLink"];
export type StaffAssignableUser = Schemas["StaffAssignableUser"];
export type StaffOrg = Schemas["StaffOrg"];
export type StaffEvent = Schemas["StaffEvent"];
export type StaffNationalIdKind = Schemas["StaffNationalIdKind"];
export type StaffNationalIdInput = Schemas["StaffNationalIdRequest"];
export type StaffNationalIdReveal = Schemas["StaffNationalIdReveal"];
export type StaffBankAccountKind = Schemas["StaffBankAccountKind"];
export type StaffBankAccountInput = Schemas["StaffBankAccountRequest"];
export type StaffBankAccountReveal = Schemas["StaffBankAccountReveal"];

/** HR's full replace of the editable fields, guarded by the revision the form was opened at. */
export const updatePerson = (id: number, input: StaffPersonInput, revision: number): Promise<StaffPerson> =>
  request<StaffPerson>(`/api/v1/staff/people/${id}`, json("PUT", input, revision));

/** The person's next of kin with the person's revision; the private group's readers alone get them. */
export const nextOfKinQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "next-of-kin"],
    queryFn: ({ signal }) => request<StaffNextOfKinList>(`/api/v1/staff/people/${id}/next-of-kin`, { signal }),
  });

/** All of them, in order — a full replace guarded by the person's revision. */
export const updateNextOfKin = (id: number, nextOfKin: StaffNextOfKin[], revision: number) =>
  request<StaffNextOfKinList>(`/api/v1/staff/people/${id}/next-of-kin`, json("PUT", { nextOfKin }, revision));

/** The roles a person holds for the company (the work group), with the person's revision. */
export const rolesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "roles"],
    queryFn: ({ signal }) => request<StaffPersonRoleList>(`/api/v1/staff/people/${id}/roles`, { signal }),
  });

export const updateRoles = (id: number, roles: StaffPersonRole[], revision: number) =>
  request<StaffPersonRoleList>(`/api/v1/staff/people/${id}/roles`, json("PUT", { roles }, revision));

/** The self-service link — the person's and HR's to read. */
export const personLinkQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "user"],
    queryFn: ({ signal }) => request<StaffPersonLink>(`/api/v1/staff/people/${id}/user`, { signal }),
  });

/** Links the person to a user, or unlinks them with null (`staff:manage`). */
export const updatePersonLink = (id: number, userId: string | null) =>
  request<StaffPersonLink>(`/api/v1/staff/people/${id}/user`, json("PUT", { userId }));

/** The active users a person may be linked to, as the user directory finds them — at most twenty. */
export const assignableUsersQueryOptions = (q: string) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "assignable-users", q],
    queryFn: ({ signal }) =>
      request<StaffAssignableUser[]>(`/api/v1/staff/assignable-users?q=${encodeURIComponent(q)}`, { signal }),
  });

/** The manager chain up and the direct reports, today. */
export const orgQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "org"],
    queryFn: ({ signal }) => request<StaffOrg>(`/api/v1/staff/people/${id}/org`, { signal }),
  });

/** One page of a person's history, newest first, shaped by who is asking. */
export const personEventsQueryOptions = (id: number, page: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", id, "events", page],
    queryFn: ({ signal }) =>
      request<Paginated<StaffEvent>>(`/api/v1/staff/people/${id}/events?page=${page}`, { signal }),
    placeholderData: keepPreviousData,
  });

// The sealed identifiers (design S4): written and revealed under
// `staff:personal-data` alone. A reveal is a POST answered `no-store`; it is
// never a query, so nothing of it lands in the query cache.

export const setNationalId = (id: number, input: StaffNationalIdInput) =>
  request<StaffPerson>(`/api/v1/staff/people/${id}/national-id`, json("PUT", input));

export const revealNationalId = (id: number) =>
  request<StaffNationalIdReveal>(`/api/v1/staff/people/${id}/national-id/reveal`, {
    method: "POST",
    cache: "no-store",
  });

export const setBankAccount = (id: number, input: StaffBankAccountInput) =>
  request<StaffPerson>(`/api/v1/staff/people/${id}/bank-account`, json("PUT", input));

export const revealBankAccount = (id: number) =>
  request<StaffBankAccountReveal>(`/api/v1/staff/people/${id}/bank-account/reveal`, {
    method: "POST",
    cache: "no-store",
  });

// The hold group (design S15): the legal hold and the reassessment are
// `staff:personal-data`'s, and the only writes an erased person still takes
// besides an unlink and a rehire (`POST …/employments`).

export type StaffLegalHold = Schemas["StaffLegalHold"];
export type StaffErasedPerson = Schemas["StaffErasedPerson"];

/** Sets the hold with its reason, or lifts it — `{held: false}` and no reason. Answers the person. */
export const setLegalHold = (id: number, held: boolean, reason?: string) =>
  request<StaffPerson>(`/api/v1/staff/people/${id}/legal-hold`, json("PUT", held ? { held, reason } : { held }));

/** Records the reassessment of an erased person; `reassessment_not_due` (409) before it is due. */
export const markReassessed = (id: number) =>
  request<StaffPerson>(`/api/v1/staff/people/${id}/reassessed`, { method: "POST" });

/**
 * The erased people with the same names, however cased, and birth date — the
 * New person warning (`staff:manage`). A body, not a query, so the names
 * stay out of access logs.
 */
export const findSimilarErased = (input: Schemas["StaffSimilarErasedRequest"]) =>
  request<StaffErasedPerson[]>("/api/v1/staff/people/similar-erased", json("POST", input));

/**
 * The complete personal-data file (`staff:personal-data`), the answer to an
 * innsyn request — named `staff-<employeeNumber>-personal-data.json` by the
 * server, and recorded in the person's history as an audited reveal.
 */
export const downloadPersonalData = (person: StaffPerson): Promise<FileDownload> =>
  downloadFile(`/api/v1/staff/people/${person.id}/personal-data`, `staff-${person.employeeNumber}-personal-data.json`);
