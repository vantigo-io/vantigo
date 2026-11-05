import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type InventoryVariantRef = Schemas["InventoryVariantRef"];
export type InventoryVariantStock = Schemas["InventoryVariantStock"];
export type InventorySiteStock = Schemas["InventorySiteStock"];
export type InventoryProductStock = Schemas["InventoryProductStock"];

/** Goods variants by SKU, product name or barcode, for the line editor's picker (≤ 50). */
export const variantSearchQueryOptions = (q: string, limit = 20) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "variants", q, limit],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryVariantList"]>(`/api/v1/inventory/variants${queryString({ q, limit })}`, {
        signal,
      }).then((answer) => answer.variants),
  });

/** A product's stock per variant, for the product page's Stock card (I27); a Service product answers no variants. */
export const productStockQueryOptions = (productId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "product", productId, "stock"],
    queryFn: ({ signal }) =>
      request<InventoryProductStock>(`/api/v1/inventory/products/${productId}/stock`, { signal }),
  });

/** One variant's stock by site and location. */
export const variantStockQueryOptions = (variantId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "variant", variantId, "stock"],
    queryFn: ({ signal }) =>
      request<InventoryVariantStock>(`/api/v1/inventory/variants/${variantId}/stock`, { signal }),
  });

export type InventoryPackaging = Schemas["InventoryPackaging"];
export type InventoryStockTotal = Schemas["InventoryStockTotal"];

/** `GET /stock` answers at most this many variants at a time (I33). */
export const MAX_STOCK_TOTAL_IDS = 100;

/** Each variant's on hand across every location, its reserved and available; a variant with neither stock nor a reservation is absent (I33). */
export const stockTotalsQueryOptions = (variantIds: readonly number[]) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "stock-totals", variantIds],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryStockTotals"]>(
        `/api/v1/inventory/stock${queryString({ variantIds: variantIds.join(",") })}`,
        { signal },
      ).then((answer) => answer.totals),
  });
