import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, request } from "./request";

/**
 * What the caller may do, answered by the server rather than re-derived from
 * permission keys here (design §5): the app hides every control the meta says
 * no to, and the server refuses it anyway.
 */
export type InventoryMeta = components["schemas"]["InventoryMeta"];

export const inventoryMetaQueryOptions = () =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<InventoryMeta>("/api/v1/inventory/meta", { signal }),
    staleTime: 60_000,
  });
