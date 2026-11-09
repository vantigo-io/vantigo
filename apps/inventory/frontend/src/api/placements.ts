import { infiniteQueryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { handleUnauthorized, INVENTORY_QUERY_KEY, queryString, refusalOf, request, send } from "./request";

type Schemas = components["schemas"];

export type InventoryPlacement = Schemas["InventoryPlacement"];
export type InventoryPlacementInput = Schemas["InventoryPlacementInput"];
export type InventoryLowStockRow = Schemas["InventoryLowStockRow"];
export type InventoryLowStockPackaging = Schemas["InventoryLowStockPackaging"];
export type InventoryPlacementImportResult = Schemas["InventoryPlacementImportResult"];
export type InventoryPlacementImportRow = Schemas["InventoryPlacementImportRow"];
export type InventoryPlacementImportOutcome = Schemas["InventoryPlacementImportOutcome"];

/** A placements row's outcomes (I61), pinned to the contract by i18n.test.ts. */
export const placementImportOutcomes: readonly InventoryPlacementImportOutcome[] = [
  "ok",
  "site_not_found",
  "site_archived",
  "placement_location_invalid",
  "variant_not_found",
  "variant_not_stockable",
  "quantity_invalid",
  "maximum_below_reorder_point",
];

/** The header a placements file must start with (I61). */
export const PLACEMENTS_IMPORT_HEADER = "sku,site,primary_location,reorder_point,maximum";

/**
 * Sets a variant's placement at a site (I61): `If-None-Match: *` creates it —
 * one already there is `revision_conflict` — and `If-Match` changes the one
 * read at that revision.
 */
export const putPlacement = (
  variantId: number,
  siteId: number,
  input: InventoryPlacementInput,
  revision: number | undefined,
) =>
  send<InventoryPlacement>(`/api/v1/inventory/variants/${variantId}/placements/${siteId}`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      ...(revision === undefined ? { "If-None-Match": "*" } : { "If-Match": String(revision) }),
    },
    body: JSON.stringify(input),
  });

/** Removes a variant's placement at a site, guarded by the revision it was read at (I54). */
export const deletePlacement = (variantId: number, siteId: number, revision: number) =>
  send<void>(`/api/v1/inventory/variants/${variantId}/placements/${siteId}`, {
    method: "DELETE",
    headers: { "If-Match": String(revision) },
  });

export interface LowStockFilter {
  siteId?: number;
  q?: string;
}

/** The low placements by site code then SKU, a page at a time by the server's cursor (I58). */
export const lowStockInfiniteQueryOptions = (filter: LowStockFilter = {}) =>
  infiniteQueryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "low-stock", filter],
    queryFn: ({ signal, pageParam }) =>
      request<Schemas["InventoryLowStockPage"]>(
        `/api/v1/inventory/low-stock${queryString({ ...filter, cursor: pageParam })}`,
        { signal },
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next,
  });

/** The low-stock list as CSV (I60), with the list's filter, every row. */
export const exportLowStock = async (filter: LowStockFilter): Promise<Blob> => {
  const response = await fetch(appUrl(`/api/v1/inventory/low-stock.csv${queryString({ ...filter })}`), {
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return response.blob();
};

/**
 * The placements import (I61): a dry run answers every row's outcome; a
 * commit writes every row, or nothing when one fails. The file goes as its
 * bytes, as the opening-balance import's does. `written` is a 201: an ok
 * commit answered 200 is the same file posted again, which wrote nothing.
 */
export const importPlacements = async (
  csv: ArrayBuffer,
  dryRun: boolean,
): Promise<{ result: InventoryPlacementImportResult; written: boolean }> => {
  const response = await fetch(appUrl(`/api/v1/inventory/imports/placements${queryString({ dryRun })}`), {
    method: "POST",
    headers: { "Content-Type": "text/csv" },
    body: csv,
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return { result: (await response.json()) as InventoryPlacementImportResult, written: response.status === 201 };
};
