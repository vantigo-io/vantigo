import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, json, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type InventoryCount = Schemas["InventoryCount"];
export type InventoryCountLine = Schemas["InventoryCountLine"];
export type InventoryCountInput = Schemas["InventoryCountInput"];
export type InventoryCountLineInput = Schemas["InventoryCountLineInput"];
export type InventoryCountPostInput = Schemas["InventoryCountPostInput"];
export type InventoryCountStatus = Schemas["InventoryCountStatus"];
export type InventoryUncounted = Schemas["InventoryUncounted"];

/** A count's statuses (I16), pinned to the contract by i18n.test.ts. */
export const countStatuses: readonly InventoryCountStatus[] = ["counting", "posted", "cancelled"];
/** What posting does with a line nobody counted (I19). */
export const uncountedChoices: readonly InventoryUncounted[] = ["keep", "zero"];

/** A `PUT /counts/{id}/lines` carries at most this many lines. */
export const MAX_COUNT_LINES_PER_SAVE = 500;

export interface CountsFilter {
  status?: InventoryCountStatus;
  siteId?: number;
}

/** The counts, newest first, at most 200 (without their lines). */
export const countsQueryOptions = (filter: CountsFilter = {}) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "counts", filter],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryCountList"]>(`/api/v1/inventory/counts${queryString({ ...filter })}`, {
        signal,
      }).then((answer) => answer.counts),
  });

/** One count with its lines, by location full code then SKU. */
export const countQueryOptions = (countId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "count", countId],
    queryFn: ({ signal }) => request<InventoryCount>(`/api/v1/inventory/counts/${countId}`, { signal }),
  });

export const openCount = (input: InventoryCountInput) =>
  send<InventoryCount>("/api/v1/inventory/counts", json("POST", input));

/** Records counted quantities, at most `MAX_COUNT_LINES_PER_SAVE` at a time; answers the count with its lines. */
export const putCountLines = (countId: number, lines: readonly InventoryCountLineInput[]) =>
  send<InventoryCount>(`/api/v1/inventory/counts/${countId}/lines`, json("PUT", { lines }));

export const postCount = (countId: number, input: InventoryCountPostInput) =>
  send<InventoryCount>(`/api/v1/inventory/counts/${countId}/post`, json("POST", input));

export const cancelCount = (countId: number) =>
  send<InventoryCount>(`/api/v1/inventory/counts/${countId}/cancel`, { method: "POST" });
