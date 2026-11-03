import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, json, queryString, request, send } from "./request";
import type { InventoryStatus } from "./sites";

type Schemas = components["schemas"];

export type InventoryLocation = Schemas["InventoryLocation"];
export type InventoryLocationRef = Schemas["InventoryLocationRef"];
export type InventoryLocationDetail = Schemas["InventoryLocationDetail"];
export type InventoryLocationInput = Schemas["InventoryLocationInput"];
export type InventoryLocationUpdate = Schemas["InventoryLocationUpdate"];
export type InventoryLocationKind = Schemas["InventoryLocationKind"];
export type InventoryBulkLevel = Schemas["InventoryBulkLevel"];
export type InventoryBulkCreateInput = Schemas["InventoryBulkCreateInput"];
export type InventoryBalanceRow = Schemas["InventoryBalanceRow"];

/** Every location kind (I2), pinned to the contract by i18n.test.ts; `site` is the root's alone. */
export const locationKinds: readonly InventoryLocationKind[] = [
  "site",
  "zone",
  "area",
  "sales_floor",
  "back_room",
  "aisle",
  "rack",
  "shelf",
  "bin",
  "display",
  "dock",
  "other",
];

/** The kinds a person may give a location: every one but the root's. */
export const writableLocationKinds = locationKinds.filter((kind) => kind !== "site");

/** A site's locations, flat and by full code; every status unless one is asked for. */
export const siteLocationsQueryOptions = (siteId: number, status?: InventoryStatus) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "site", siteId, "locations", status ?? "all"],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryLocationList"]>(
        `/api/v1/inventory/sites/${siteId}/locations${queryString({ status })}`,
        { signal },
      ).then((answer) => answer.locations),
  });

/** A location with its site, its path from the root and its children (I29). */
export const locationQueryOptions = (locationId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "location", locationId],
    queryFn: ({ signal }) => request<InventoryLocationDetail>(`/api/v1/inventory/locations/${locationId}`, { signal }),
  });

/** What lies on the location, and below it unless `subtree` is false. */
export const locationContentsQueryOptions = (locationId: number, subtree: boolean) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "location", locationId, "contents", subtree],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryLocationContents"]>(
        `/api/v1/inventory/locations/${locationId}/contents${queryString({ subtree: subtree ? undefined : false })}`,
        { signal },
      ).then((answer) => answer.rows),
  });

export const createLocation = (input: InventoryLocationInput) =>
  send<InventoryLocation>("/api/v1/inventory/locations", json("POST", input));

/** A grid under a parent (I5), all or nothing. */
export const bulkCreateLocations = (input: InventoryBulkCreateInput) =>
  send<Schemas["InventoryBulkCreated"]>("/api/v1/inventory/locations/bulk", json("POST", input)).then(
    (answer) => answer.created,
  );

export const updateLocation = (locationId: number, input: InventoryLocationUpdate, revision: number) =>
  send<InventoryLocation>(`/api/v1/inventory/locations/${locationId}`, json("PUT", input, revision));

export const deleteLocation = (locationId: number) =>
  send<void>(`/api/v1/inventory/locations/${locationId}`, { method: "DELETE" });

/** A location's fields as an update, so archiving or reactivating sends everything else back unchanged. */
export const locationUpdateOf = (location: InventoryLocation): InventoryLocationUpdate => ({
  code: location.code,
  kind: location.kind,
  sortOrder: location.sortOrder,
  status: location.status,
  ...(location.name ? { name: location.name } : {}),
  ...(location.parentId === undefined ? {} : { parentId: location.parentId }),
});

/** How many locations a bulk create makes: the product of each level's range. */
export const bulkCount = (levels: readonly InventoryBulkLevel[]) =>
  levels.reduce((total, level) => total * rangeSize(level), levels.length > 0 ? 1 : 0);

/** The number of codes one level's range spans (`01`–`12` is 12, `A`–`D` is 4); 0 when the range is not one. */
export const rangeSize = ({ from, to }: InventoryBulkLevel) => {
  if (!from) return 0;
  if (!to) return 1;
  if (/^\d+$/.test(from) && /^\d+$/.test(to)) return Math.max(0, Number(to) - Number(from) + 1);
  if (/^[A-Z]$/i.test(from) && /^[A-Z]$/i.test(to))
    return Math.max(0, to.toUpperCase().charCodeAt(0) - from.toUpperCase().charCodeAt(0) + 1);
  return 0;
};
