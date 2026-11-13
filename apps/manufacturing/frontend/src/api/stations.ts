import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import stationOperations from "../station-operations.json";
import { json, MANUFACTURING_QUERY_KEY, request, send } from "./request";

export type ManufacturingStationWorkCenters = components["schemas"]["ManufacturingStationWorkCenters"];

/**
 * Every request a shared station's page may make (manufacturing phase 2
 * M135): Manufacturing's operations marked `x-vantigo-station` and Identity's
 * station endpoints. A server test holds the file to the contracts; the
 * station page's test holds every request the page makes to it.
 */
export const stationOperationList: readonly { operationId: string; method: string; path: string }[] = [
  ...stationOperations.manufacturing,
  ...stationOperations.identity,
];

/** The work centers a station serves (M131). */
export const stationWorkCentersQueryOptions = (stationId: string) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "stations", stationId, "work-centers"],
    queryFn: ({ signal }) =>
      request<ManufacturingStationWorkCenters>(`/api/v1/manufacturing/stations/${stationId}/work-centers`, {
        signal,
      }).then((answer) => answer.workCenterIds),
  });

/** Replaces the work centers a station serves (`manufacturing:boms-manage`). */
export const setStationWorkCenters = (stationId: string, workCenterIds: readonly number[]) =>
  send<ManufacturingStationWorkCenters>(
    `/api/v1/manufacturing/stations/${stationId}/work-centers`,
    json("PUT", { workCenterIds }),
  );
