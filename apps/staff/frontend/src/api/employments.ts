import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

export type StaffEmployment = Schemas["StaffEmployment"];
export type StaffEmploymentInput = Schemas["StaffEmploymentRequest"];
export type StaffTermsInput = Schemas["StaffTermsRequest"];
export type StaffEmploymentType = Schemas["StaffEmploymentType"];
export type StaffAppointmentForm = Schemas["StaffAppointmentForm"];
export type StaffWorkingHoursScheme = Schemas["StaffWorkingHoursScheme"];
export type StaffTemporaryGround = Schemas["StaffTemporaryGround"];
export type StaffRelationshipType = Schemas["StaffRelationshipType"];
export type StaffEmployeeType = Schemas["StaffEmployeeType"];
export type StaffNoticeUnit = Schemas["StaffNoticeUnit"];
export type StaffRemunerationType = Schemas["StaffRemunerationType"];
export type StaffGender = Schemas["StaffGender"];

/**
 * The second step of New person: an employment with its first terms row,
 * dated the start. Left out, the business unit is the default one and the
 * employment number `<employee number>-<n>`.
 */
export const createEmployment = (personId: number, input: StaffEmploymentInput): Promise<StaffEmployment> =>
  request<StaffEmployment>(`/api/v1/staff/people/${personId}/employments`, json("POST", input));

export type StaffTerms = Schemas["StaffTerms"];
export type StaffPayComponent = Schemas["StaffPayComponent"];
export type StaffPayPeriod = Schemas["StaffPayPeriod"];
export type StaffEndReason = Schemas["StaffEndReason"];
export type StaffEmploymentChangeInput = Schemas["StaffEmploymentChangeRequest"];
export type StaffEmploymentEndInput = Schemas["StaffEmploymentEndRequest"];
export type StaffContractCheck = Schemas["StaffContractCheck"];
export type StaffContractCheckItem = Schemas["StaffContractCheckItem"];

/** A person's employments, oldest first, shaped by who is asking. */
export const personEmploymentsQueryOptions = (personId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "people", "detail", personId, "employments"],
    queryFn: ({ signal }) => request<StaffEmployment[]>(`/api/v1/staff/people/${personId}/employments`, { signal }),
  });

/** An employment's terms, oldest first as the server answers them. */
export const termsQueryOptions = (employmentId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "employments", employmentId, "terms"],
    queryFn: ({ signal }) => request<StaffTerms[]>(`/api/v1/staff/employments/${employmentId}/terms`, { signal }),
  });

/** The § 14-6 check of the row in force today; the employment group's readers alone get it. */
export const contractCheckQueryOptions = (employmentId: number) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "employments", employmentId, "contract-check"],
    queryFn: ({ signal }) =>
      request<StaffContractCheck>(`/api/v1/staff/employments/${employmentId}/contract-check`, { signal }),
  });

export const updateEmployment = (id: number, input: StaffEmploymentChangeInput, revision: number) =>
  request<StaffEmployment>(`/api/v1/staff/employments/${id}`, json("PUT", input, revision));

export const endEmployment = (id: number, input: StaffEmploymentEndInput) =>
  request<StaffEmployment>(`/api/v1/staff/employments/${id}/end`, json("POST", input));

export const reopenEmployment = (id: number) =>
  request<StaffEmployment>(`/api/v1/staff/employments/${id}/reopen`, { method: "POST" });

/** A new row on its `validFrom`: a field left out takes the row before's value, one sent as null is cleared. */
export const addTerms = (employmentId: number, input: StaffTermsInput) =>
  request<StaffTerms>(`/api/v1/staff/employments/${employmentId}/terms`, json("POST", input));

/** A row replaced whole: the latest, or any row with `correction: true`. */
export const updateTerms = (id: number, input: StaffTermsInput, revision: number) =>
  request<StaffTerms>(`/api/v1/staff/terms/${id}`, json("PUT", input, revision));

/** The latest row only, never the first. */
export const deleteTerms = (id: number) => request<void>(`/api/v1/staff/terms/${id}`, { method: "DELETE" });
