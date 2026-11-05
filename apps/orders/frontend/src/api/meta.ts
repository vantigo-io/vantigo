import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";
import type { Meta } from "./types";

/**
 * What the editors need of the settings under `orders:access` alone: whether
 * Send works now (O25), the default validity and payment terms.
 */
export const metaQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<Meta>("/api/v1/orders/meta", { signal }),
    staleTime: 60_000,
  });
