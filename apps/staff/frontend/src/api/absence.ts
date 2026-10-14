import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { type Paginated, refusalOf, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

/**
 * An absence as the server shaped it for the caller (design §5): a field the
 * caller may not read is absent, so the page draws what is there and nothing
 * else — no type for a colleague, "leave" for an HR-only type to a manager.
 */
export type StaffAbsence = Schemas["StaffAbsence"];
export type StaffAbsenceStatus = Schemas["StaffAbsenceStatus"];
export type StaffAbsenceInput = Schemas["StaffAbsenceChangeRequest"];
export type StaffHolidayAccount = Schemas["StaffHolidayAccount"];
export type StaffSelfCertification = Schemas["StaffSelfCertification"];
export type StaffSelfCertPolicy = Schemas["StaffSelfCertPolicy"];
export type StaffCareDays = Schemas["StaffCareDays"];
export type StaffCalendar = Schemas["StaffCalendar"];
export type StaffApproval = Schemas["StaffApproval"];

/** The most rows a page of this module's lists holds. */
const MAX_PAGE_SIZE = 100;

/**
 * A write of absence, fetched here rather than through the shared client,
 * which drops a validation refusal's code: `percent_not_allowed`,
 * `hours_on_day_type` and `absence_note_on_sick` are field errors whose code
 * the sheet words, as it words every 409's. A case's writes go through it
 * too.
 */
export const sendAbsenceWrite = async <T>(
  path: string,
  method: string,
  body?: unknown,
  ifMatch?: string,
): Promise<T> => {
  const response = await fetch(appUrl(path), {
    method,
    credentials: "include",
    headers: {
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      ...(ifMatch === undefined ? {} : { "If-Match": ifMatch }),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  if (response.ok) return (response.status === 204 ? undefined : await response.json()) as T;
  throw await refusalOf(response);
};

/**
 * What an absence's change sends back in If-Match: the revision to the person
 * and to HR with `staff:personal-data`, the opaque etag — quoted — to HR
 * without it. The row carries the one its reader is sent; one carrying
 * neither is not the reader's to change.
 */
export const absenceIfMatch = (absence: StaffAbsence): string | undefined =>
  absence.etag ? `"${absence.etag}"` : absence.revision === undefined ? undefined : String(absence.revision);

/** My own absences, newest first (`GET /me/absences`). */
export const myAbsencesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "absences"],
    queryFn: ({ signal }) =>
      request<Paginated<StaffAbsence>>(`/api/v1/staff/me/absences?pageSize=${MAX_PAGE_SIZE}`, { signal }),
  });

/** My own holiday account for a year. */
export const myHolidayAccountQueryOptions = (year: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "holiday-account", year],
    queryFn: ({ signal }) => request<StaffHolidayAccount>(`/api/v1/staff/me/holiday-account?year=${year}`, { signal }),
  });

/** My own egenmelding standing today. */
export const mySelfCertificationQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "self-certification"],
    queryFn: ({ signal }) => request<StaffSelfCertification>("/api/v1/staff/me/self-certification", { signal }),
  });

/** My own care days for a year; a refusal leaves the card out. */
export const myCareDaysQueryOptions = (year: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "me", "care-days", year],
    queryFn: ({ signal }) => request<StaffCareDays>(`/api/v1/staff/me/care-days?year=${year}`, { signal }),
    retry: false,
  });

/** The absence calendar of at most 93 days, with the public holidays in it (`internal/calendar`). */
export const calendarQueryOptions = (from: string, to: string) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "calendar", from, to],
    queryFn: ({ signal }) =>
      request<StaffCalendar>(`/api/v1/staff/calendar?${new URLSearchParams({ from, to })}`, { signal }),
    staleTime: 5 * 60 * 1000,
  });

/** The requests I decide, oldest first. */
export const approvalsQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "approvals"],
    queryFn: ({ signal }) =>
      request<Paginated<StaffApproval>>(`/api/v1/staff/approvals?pageSize=${MAX_PAGE_SIZE}`, { signal }),
  });

/** Requests my own absence, or registers it when its type is a notification. */
export const requestMyAbsence = (input: StaffAbsenceInput): Promise<StaffAbsence> =>
  sendAbsenceWrite<StaffAbsence>("/api/v1/staff/me/absences", "POST", input);

/** Replaces a request's fields, guarded by the token the row carries. */
export const updateAbsence = (absence: StaffAbsence, input: StaffAbsenceInput): Promise<StaffAbsence> =>
  sendAbsenceWrite<StaffAbsence>(`/api/v1/staff/absences/${absence.id}`, "PUT", input, absenceIfMatch(absence));

/** Withdraws a request and keeps it as history. */
export const cancelAbsence = (id: number): Promise<StaffAbsence> =>
  sendAbsenceWrite<StaffAbsence>(`/api/v1/staff/absences/${id}/cancel`, "POST");

export const approveAbsence = (id: number): Promise<StaffAbsence> =>
  sendAbsenceWrite<StaffAbsence>(`/api/v1/staff/absences/${id}/approve`, "POST", {});

/** A rejection says why: the note is required. */
export const rejectAbsence = (id: number, note: string): Promise<StaffAbsence> =>
  sendAbsenceWrite<StaffAbsence>(`/api/v1/staff/absences/${id}/reject`, "POST", { note });
