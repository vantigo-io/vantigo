import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";
import type { Brand, BrandOption } from "./types";

export type { Brand, BrandOption };

/**
 * The brands a quote or an order can be printed in. A short list that changes
 * when someone publishes or archives a brand, so half a minute of staleness is
 * not visible.
 */
export const brandsQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "brands"],
    queryFn: ({ signal }) => request<BrandOption[]>("/api/v1/orders/brands", { signal }),
    staleTime: 30_000,
  });
