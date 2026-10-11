import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, request, STAFF_QUERY_KEY } from "./request";

type Schemas = components["schemas"];

export type StaffLegalEntity = Schemas["StaffLegalEntity"];
export type StaffLegalEntityInput = Schemas["StaffLegalEntityRequest"];
export type StaffBusinessUnit = Schemas["StaffBusinessUnit"];
export type StaffBusinessUnitInput = Schemas["StaffBusinessUnitRequest"];
export type StaffDepartment = Schemas["StaffDepartment"];
export type StaffDepartmentInput = Schemas["StaffDepartmentRequest"];
export type StaffLocation = Schemas["StaffLocation"];
export type StaffLocationInput = Schemas["StaffLocationRequest"];

/**
 * The four structure tables (design S6) share one shape of operations: list
 * (archived rows included, with `archivedAt`), add, a revision-guarded full
 * replace, and archive. Nothing is ever deleted — a row other rows name stays
 * nameable.
 */
export type StructureKind = "legal-entities" | "business-units" | "departments" | "locations";

interface StructureRows {
  "legal-entities": StaffLegalEntity;
  "business-units": StaffBusinessUnit;
  departments: StaffDepartment;
  locations: StaffLocation;
}

interface StructureInputs {
  "legal-entities": StaffLegalEntityInput;
  "business-units": StaffBusinessUnitInput;
  departments: StaffDepartmentInput;
  locations: StaffLocationInput;
}

export const structureQueryOptions = <K extends StructureKind>(kind: K) =>
  queryOptions({
    queryKey: [STAFF_QUERY_KEY, "structure", kind],
    queryFn: ({ signal }) => request<StructureRows[K][]>(`/api/v1/staff/${kind}`, { signal }),
  });

export const createStructureRow = <K extends StructureKind>(kind: K, input: StructureInputs[K]) =>
  request<StructureRows[K]>(`/api/v1/staff/${kind}`, json("POST", input));

export const updateStructureRow = <K extends StructureKind>(
  kind: K,
  id: number,
  input: StructureInputs[K],
  revision: number,
) => request<StructureRows[K]>(`/api/v1/staff/${kind}/${id}`, json("PUT", input, revision));

/** Refused `structure_default` (409) on the default row — another row is made the default first. */
export const archiveStructureRow = <K extends StructureKind>(kind: K, id: number) =>
  request<StructureRows[K]>(`/api/v1/staff/${kind}/${id}/archive`, { method: "POST" });
