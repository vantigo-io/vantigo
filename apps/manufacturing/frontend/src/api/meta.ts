import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { MANUFACTURING_QUERY_KEY, request } from "./request";

/**
 * What the caller may do, answered by the server rather than re-derived from
 * permission keys here: the app hides every control the meta says no to, and
 * the server refuses it anyway. `canSeeCosts` decides whether any cost field
 * is shown — and whether a work center's rates are sent at all (M38).
 */
export type ManufacturingMeta = components["schemas"]["ManufacturingMeta"];

export const manufacturingMetaQueryOptions = () =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<ManufacturingMeta>("/api/v1/manufacturing/meta", { signal }),
    staleTime: 60_000,
  });
