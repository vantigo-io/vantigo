import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { MANUFACTURING_QUERY_KEY, request } from "./request";

export type ManufacturingStats = components["schemas"]["ManufacturingStats"];

/** The overview's and the dashboard card's counts: active and blocked work centers, active BoMs. */
export const fetchManufacturingStats = (signal?: AbortSignal) =>
  request<ManufacturingStats>("/api/v1/manufacturing/stats", { signal });

export const manufacturingStatsQueryOptions = () =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "stats"],
    queryFn: ({ signal }) => fetchManufacturingStats(signal),
  });
