import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, MANUFACTURING_QUERY_KEY, request, send } from "./request";

type Schemas = components["schemas"];

export type ManufacturingClosure = Schemas["ManufacturingClosure"];
export type ManufacturingClosureInput = Schemas["ManufacturingClosureInput"];

/** At most this many closures per work center (M9, `closures_too_many`). */
export const MAX_CLOSURES = 100;

/** A work center's closures by their first day. */
export const closuresQueryOptions = (workCenterId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "closures", workCenterId],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingClosureList"]>(`/api/v1/manufacturing/work-centers/${workCenterId}/closures`, {
        signal,
      }).then((answer) => answer.items),
  });

export const createClosure = (workCenterId: number, input: ManufacturingClosureInput) =>
  send<ManufacturingClosure>(`/api/v1/manufacturing/work-centers/${workCenterId}/closures`, json("POST", input));

export const deleteClosure = (workCenterId: number, closureId: number) =>
  send<void>(`/api/v1/manufacturing/work-centers/${workCenterId}/closures/${closureId}`, { method: "DELETE" });
