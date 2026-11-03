import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, request } from "./request";

export type InventoryStats = components["schemas"]["InventoryStats"];

/** The dashboard card's figures: sites, locations, stock lines and this week's receipts. */
export const fetchInventoryStats = (signal?: AbortSignal) =>
  request<InventoryStats>("/api/v1/inventory/stats", { signal });

export const inventoryStatsQueryOptions = () =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "stats"],
    queryFn: ({ signal }) => fetchInventoryStats(signal),
  });
