import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";
import type { Variant, VariantPackaging } from "./types";

/**
 * A sellable variant as the line editor's picker offers it, from Orders' own
 * catalog search (`orders:access`): its snapshot, its list price in the
 * document's currency today and its VAT code (O16). The server prices the
 * line again when it is saved.
 */
export interface VariantOption {
  variantId: number;
  /** The variant's product, whose variants Add from grid lays out (O34). */
  productId: number;
  sku: string;
  name: string;
  unit: string;
  price: number | null;
  /** The packagings a line may be entered in (O15). */
  packagings: VariantPackaging[];
}

export const variantOptionOf = (v: Variant): VariantOption => ({
  variantId: v.id,
  productId: v.productId,
  sku: v.sku,
  name: v.name,
  unit: v.unit,
  price: v.price ?? null,
  packagings: v.packagings,
});

/** The active goods and services matching the words, in the document's currency. */
export const variantSearchQueryOptions = (search: string, currency: string) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "variants", search, currency],
    queryFn: async ({ signal }): Promise<VariantOption[]> => {
      const params = new URLSearchParams({ q: search.trim(), currency, limit: "20" });
      const answer = await request<{ items: Variant[] }>(`/api/v1/orders/variants?${params.toString()}`, { signal });
      return answer.items.map(variantOptionOf);
    },
    staleTime: 30_000,
  });
