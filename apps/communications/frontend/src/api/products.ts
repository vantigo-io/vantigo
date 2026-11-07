import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { request } from "./request";

/**
 * What the product picker reads of Products (Messenger design N16): on the Products API with the
 * user's own permissions, so a user without products:products-view never reaches it. Only the fields the
 * picker uses are typed.
 */
export type PickerPrice = { amount: number; currency: string };
export type PickerProduct = {
  id: number;
  name: string;
  sku?: string | null;
  status: string;
  /** Empty for a reader without products:pricing-view. */
  effectivePrices: PickerPrice[];
  variants: { effectivePrices: PickerPrice[] }[];
};
export type PickerImage = {
  id: number;
  productId: number;
  altText?: string | null;
  isPrimary: boolean;
  sortOrder: number;
  uses?: string[] | null;
};

/** The most products the picker lists at once. */
export const PICKER_PAGE_SIZE = 8;

/** The active products matching the words, by name. */
export const pickerProductsQueryOptions = (search: string) =>
  queryOptions({
    queryKey: ["communications", "picker-products", search],
    queryFn: async ({ signal }): Promise<PickerProduct[]> => {
      const params = new URLSearchParams({ pageSize: String(PICKER_PAGE_SIZE), status: "Active", sortBy: "name" });
      if (search.trim()) params.set("search", search.trim());
      const answer = await request<{ data: PickerProduct[] }>(`/api/v1/products?${params.toString()}`, { signal });
      return answer.data;
    },
    staleTime: 30_000,
  });

/** A product's images in their order. */
export const pickerImagesQueryOptions = (productId: number) =>
  queryOptions({
    queryKey: ["communications", "picker-images", productId],
    queryFn: ({ signal }) => request<PickerImage[]>(`/api/v1/products/${productId}/images`, { signal }),
    staleTime: 30_000,
  });

/** The images marked for Communications, in their order: the ones a message may show. */
export const communicationsImages = (images: readonly PickerImage[]): PickerImage[] =>
  images.filter((image) => image.uses?.includes("communications")).sort((a, b) => a.sortOrder - b.sortOrder);

/** An image's thumbnail, which the page shows as it shows any of Products' images. */
export const pickerThumbnailUrl = (productId: number, imageId: number): string =>
  appUrl(`/api/v1/products/${productId}/images/${imageId}/content?size=thumb`);

/** The price a card's subtitle is prefilled with: the product's own, else its first variant's; null when it has none. */
export const pickerPriceOf = (product: PickerProduct): PickerPrice | null =>
  product.effectivePrices[0] ??
  product.variants.find((variant) => variant.effectivePrices.length > 0)?.effectivePrices[0] ??
  null;
