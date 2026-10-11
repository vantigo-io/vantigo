import type { components } from "../api-schema";
import { json, request } from "./request";

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
