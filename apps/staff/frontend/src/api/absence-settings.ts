import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { sendAbsenceWrite } from "./absence";
import { request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

export type StaffAbsenceTypeInput = Schemas["StaffAbsenceTypeRequest"];
export type StaffAbsenceTypeChange = Schemas["StaffAbsenceTypeChangeRequest"];
export type StaffHolidaySchemeInput = Schemas["StaffHolidaySchemeRequest"];
export type StaffHolidaySchemeChange = Schemas["StaffHolidaySchemeChangeRequest"];
export type StaffAmeldingLeave = Schemas["StaffAmeldingLeave"];

/** Every absence type — the hidden ones and the reserved `absent` too, for staff:manage. */
export const absenceTypesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "absence-types"],
    queryFn: ({ signal }) => request<Schemas["StaffAbsenceType"][]>("/api/v1/staff/absence-types", { signal }),
  });

/** Every holiday scheme, archived ones included, with its figures converted to working days. */
export const holidaySchemesQueryOptions = () =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "holiday-schemes"],
    queryFn: ({ signal }) => request<Schemas["StaffHolidayScheme"][]>("/api/v1/staff/holiday-schemes", { signal }),
  });

/*
 * The writes go through `sendAbsenceWrite`, which keeps a validation
 * refusal's code, so `seed_immutable`, `absence_type_in_use` and
 * `holiday_scheme_in_use` are worded by code.
 */
export const createAbsenceType = (input: StaffAbsenceTypeInput) =>
  sendAbsenceWrite<Schemas["StaffAbsenceType"]>("/api/v1/staff/absence-types", "POST", input);

/** A full replace but for the key, kind and unit, guarded by the type's revision. */
export const updateAbsenceType = (id: number, input: StaffAbsenceTypeChange, revision: number) =>
  sendAbsenceWrite<Schemas["StaffAbsenceType"]>(`/api/v1/staff/absence-types/${id}`, "PUT", input, String(revision));

export const createHolidayScheme = (input: StaffHolidaySchemeInput) =>
  sendAbsenceWrite<Schemas["StaffHolidayScheme"]>("/api/v1/staff/holiday-schemes", "POST", input);

/** A full replace — archived and restored with it — guarded by the scheme's revision. */
export const updateHolidayScheme = (id: number, input: StaffHolidaySchemeChange, revision: number) =>
  sendAbsenceWrite<Schemas["StaffHolidayScheme"]>(
    `/api/v1/staff/holiday-schemes/${id}`,
    "PUT",
    input,
    String(revision),
  );
