import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";

/**
 * A sellable variant as the line editor's picker offers it, read from the
 * products module's own list over HTTP (module boundaries rule 7). The server
 * prices the line from the catalog when it is saved (O16), so the picker
 * needs only what names the variant.
 */
export interface VariantOption {
  variantId: number;
  sku: string;
  name: string;
  unit: string;
}

interface ProductAnswer {
  id: number;
  name: string;
  unit?: string | null;
  description?: string | null;
  variants: { id: number; sku: string; unit: string; optionValues: Record<string, string> }[];
}

/** The variant's own name: the product's, with its option values when it has any. */
const variantName = (product: ProductAnswer, optionValues: Record<string, string>) => {
  const options = Object.values(optionValues).filter(Boolean);
  return options.length > 0 ? `${product.name} – ${options.join(", ")}` : product.name;
};

/** The active products matching the words, one option per variant (`products:products-view`). */
export const variantSearchQueryOptions = (search: string) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "variants", search],
    queryFn: async ({ signal }): Promise<VariantOption[]> => {
      const params = new URLSearchParams({ pageSize: "20", status: "active", search: search.trim() });
      const answer = await request<{ data: ProductAnswer[] }>(`/api/v1/products?${params.toString()}`, { signal });
      return answer.data.flatMap((product) =>
        product.variants.map((variant) => ({
          variantId: variant.id,
          sku: variant.sku,
          name: variantName(product, variant.optionValues),
          unit: variant.unit,
        })),
      );
    },
    staleTime: 30_000,
  });
