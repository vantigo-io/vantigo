import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, json, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type InventorySite = Schemas["InventorySite"];
export type InventorySiteRef = Schemas["InventorySiteRef"];
export type InventorySiteInput = Schemas["InventorySiteInput"];
export type InventorySiteUpdate = Schemas["InventorySiteUpdate"];
export type InventorySiteKind = Schemas["InventorySiteKind"];
export type InventoryStatus = Schemas["InventoryStatus"];
export type InventoryAddress = Schemas["InventoryAddress"];

/** The site kinds (I1), pinned to the contract by i18n.test.ts. */
export const siteKinds: readonly InventorySiteKind[] = ["warehouse", "store"];
/** A site's or a location's status (I6). */
export const statuses: readonly InventoryStatus[] = ["active", "archived"];

/** The sites by code; the active ones unless another status is asked for. */
export const sitesQueryOptions = (status?: InventoryStatus) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "sites", status ?? "active"],
    queryFn: ({ signal }) =>
      request<Schemas["InventorySiteList"]>(`/api/v1/inventory/sites${queryString({ status })}`, { signal }).then(
        (answer) => answer.sites,
      ),
  });

export const siteQueryOptions = (siteId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "site", siteId],
    queryFn: ({ signal }) => request<InventorySite>(`/api/v1/inventory/sites/${siteId}`, { signal }),
  });

export const createSite = (input: InventorySiteInput) =>
  send<InventorySite>("/api/v1/inventory/sites", json("POST", input));

/** Replaces the site whole, guarded by the revision it was read at. */
export const updateSite = (siteId: number, input: InventorySiteUpdate, revision: number) =>
  send<InventorySite>(`/api/v1/inventory/sites/${siteId}`, json("PUT", input, revision));

export const deleteSite = (siteId: number) => send<void>(`/api/v1/inventory/sites/${siteId}`, { method: "DELETE" });

/** A site's fields as an update, so archiving or reactivating sends everything else back unchanged. */
export const siteUpdateOf = (site: InventorySite): InventorySiteUpdate => ({
  code: site.code,
  name: site.name,
  kind: site.kind,
  status: site.status,
  ...(site.address ? { address: site.address } : {}),
});
