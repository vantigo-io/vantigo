import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * A cross-module read. Billing lines pin to a product variant, so the picker calls
 * the products HTTP API — projects never imports the products frontend. The list
 * endpoint embeds each product's variants, so one call answers the picker.
 */
export interface ServiceVariantOption {
  variantId: number;
  productName: string;
  sku: string;
  unit: string;
}

interface ProductVariantRow {
  id: number;
  sku: string;
  unit: string;
}

interface ProductRow {
  id: number;
  name: string;
  /** 'Service' or 'Good'; only services can be billed as project work (D15). */
  type: string;
  variants?: ProductVariantRow[];
}

interface ProductListResponse {
  data: ProductRow[];
}

const SERVICE_SEARCH_PAGE_SIZE = 20;

/** The billing line's variant picker: service products flattened to their variants. */
export const serviceVariantSearchQueryOptions = (search: string) => {
  const term = search.trim();
  return queryOptions({
    queryKey: ["projects", "service-variants", term],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({ page: "1", pageSize: String(SERVICE_SEARCH_PAGE_SIZE) });
      if (term) query.set("search", term);
      const page = await request<ProductListResponse>(`/api/v1/products?${query}`, { signal });
      return page.data
        .filter((product) => product.type === "Service")
        .flatMap((product) =>
          (product.variants ?? []).map((variant) => ({
            variantId: variant.id,
            productName: product.name,
            sku: variant.sku,
            unit: variant.unit,
          })),
        );
    },
    placeholderData: keepPreviousData,
  });
};
