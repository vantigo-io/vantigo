import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, MANUFACTURING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type ManufacturingWorkCenter = Schemas["ManufacturingWorkCenter"];
export type ManufacturingWorkCenterInput = Schemas["ManufacturingWorkCenterInput"];
export type ManufacturingWorkCenterUpdate = Schemas["ManufacturingWorkCenterUpdate"];
export type ManufacturingWeekPattern = Schemas["ManufacturingWeekPattern"];
export type ManufacturingInterval = Schemas["ManufacturingInterval"];
export type ManufacturingStatus = Schemas["ManufacturingStatus"];
export type ManufacturingStatusFilter = Schemas["ManufacturingStatusFilter"];
export type ManufacturingWorkingState = Schemas["ManufacturingWorkingState"];

/** A work center's or a BoM's status (M8, M10), pinned to the contract by i18n.test.ts. */
export const statuses: readonly ManufacturingStatus[] = ["active", "archived"];
/** What a list filters on: a status, or every one. */
export const statusFilters: readonly ManufacturingStatusFilter[] = ["active", "archived", "all"];
/** A work center's working state (M30), changed only by block and unblock. */
export const workingStates: readonly ManufacturingWorkingState[] = ["normal", "blocked"];

export interface WorkCenterFilter {
  status?: ManufacturingStatusFilter;
  siteId?: number;
  q?: string;
}

/** The work centers by code; the active ones unless another status is asked for. */
export const workCentersQueryOptions = (filter: WorkCenterFilter = {}) =>
  queryOptions({
    queryKey: [
      MANUFACTURING_QUERY_KEY,
      "work-centers",
      filter.status ?? "active",
      filter.siteId ?? null,
      filter.q ?? "",
    ],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingWorkCenterList"]>(
        `/api/v1/manufacturing/work-centers${queryString({ status: filter.status, siteId: filter.siteId, q: filter.q })}`,
        { signal },
      ).then((answer) => answer.items),
  });

export const workCenterQueryOptions = (workCenterId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "work-center", workCenterId],
    queryFn: ({ signal }) =>
      request<ManufacturingWorkCenter>(`/api/v1/manufacturing/work-centers/${workCenterId}`, { signal }),
  });

export const createWorkCenter = (input: ManufacturingWorkCenterInput) =>
  send<ManufacturingWorkCenter>("/api/v1/manufacturing/work-centers", json("POST", input));

/** Replaces the work center's fields under the revision it was read at; absent cost rates keep theirs. */
export const updateWorkCenter = (workCenterId: number, input: ManufacturingWorkCenterUpdate) =>
  send<ManufacturingWorkCenter>(`/api/v1/manufacturing/work-centers/${workCenterId}`, json("PUT", input));

/** Deletes a work center no operation names (`work_center_in_use` otherwise: archive it instead). */
export const deleteWorkCenter = (workCenterId: number, revision: number) =>
  send<void>(`/api/v1/manufacturing/work-centers/${workCenterId}${queryString({ revision })}`, { method: "DELETE" });

export const archiveWorkCenter = (workCenterId: number, revision: number) =>
  send<ManufacturingWorkCenter>(
    `/api/v1/manufacturing/work-centers/${workCenterId}/archive`,
    json("POST", { revision }),
  );

export const reactivateWorkCenter = (workCenterId: number, revision: number) =>
  send<ManufacturingWorkCenter>(
    `/api/v1/manufacturing/work-centers/${workCenterId}/reactivate`,
    json("POST", { revision }),
  );

export type ManufacturingWorkCenterGroup = Schemas["ManufacturingWorkCenterGroup"];
export type ManufacturingWorkCenterGroupInput = Schemas["ManufacturingWorkCenterGroupInput"];
export type ManufacturingWorkCenterGroupUpdate = Schemas["ManufacturingWorkCenterGroupUpdate"];

/** The work center groups by code (manufacturing phase 2 M117); the active ones unless another status is asked for. */
export const workCenterGroupsQueryOptions = (filter: Pick<WorkCenterFilter, "status" | "siteId"> = {}) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "work-center-groups", filter.status ?? "active", filter.siteId ?? null],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingWorkCenterGroupList"]>(
        `/api/v1/manufacturing/work-center-groups${queryString({ status: filter.status, siteId: filter.siteId })}`,
        { signal },
      ).then((answer) => answer.items),
  });

export const createWorkCenterGroup = (input: ManufacturingWorkCenterGroupInput) =>
  send<ManufacturingWorkCenterGroup>("/api/v1/manufacturing/work-center-groups", json("POST", input));

/** Replaces the group's fields and members under the revision it was read at. */
export const updateWorkCenterGroup = (groupId: number, input: ManufacturingWorkCenterGroupUpdate) =>
  send<ManufacturingWorkCenterGroup>(`/api/v1/manufacturing/work-center-groups/${groupId}`, json("PUT", input));

/** Archives a group no active BoM targets (`work_center_in_use` otherwise). */
export const archiveWorkCenterGroup = (groupId: number, revision: number) =>
  send<ManufacturingWorkCenterGroup>(
    `/api/v1/manufacturing/work-center-groups/${groupId}/archive`,
    json("POST", { revision }),
  );

export const reactivateWorkCenterGroup = (groupId: number, revision: number) =>
  send<ManufacturingWorkCenterGroup>(
    `/api/v1/manufacturing/work-center-groups/${groupId}/reactivate`,
    json("POST", { revision }),
  );
