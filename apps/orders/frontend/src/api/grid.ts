import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";
import type { VariantGrid } from "./types";

/**
 * A product's variants for Add from grid (O34): a matrix along its one or two
 * options, or a list when it has more, an option with more than 30 values or
 * a variant without a value; each with its list price in the currency today.
 */
export const variantGridQueryOptions = (productId: number, currency: string) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "grid", productId, currency],
    queryFn: ({ signal }) =>
      request<VariantGrid>(`/api/v1/orders/products/${productId}/grid?currency=${encodeURIComponent(currency)}`, {
        signal,
      }),
    staleTime: 30_000,
  });
