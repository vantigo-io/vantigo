import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import { NotFoundError, request } from "./request";

export { ApiConflictError, ApiValidationError, NotFoundError } from "./request";
export const PRODUCT_TYPES = ["Goods", "Service"] as const;
export type ProductType = (typeof PRODUCT_TYPES)[number];
export type ProductStatus = "Draft" | "Active" | "Discontinued";

export interface PriceRow {
  id: number;
  currency: string;
  amount: number;
  validFrom: string | null;
  validTo: string | null;
}
export interface PriceInput {
  currency: string;
  amount: number;
  validFrom?: string;
  validTo?: string;
}
export interface ProductCategoryRef {
  id: number;
  name: string;
}
export interface TaxCategoryRef {
  id: number;
  name: string;
  kind: string;
  rate: number;
}
// Optional fields mirror api-schema.d.ts: the server omits an empty value
// rather than sending null, so readers must treat undefined and null alike.
export interface VariantResponse {
  id: number;
  sku: string;
  barcode?: string | null;
  unit: string;
  standardCost?: number | null;
  weightKg?: number | null;
  lengthCm?: number | null;
  widthCm?: number | null;
  heightCm?: number | null;
  optionValues: Record<string, string>;
  effectivePrices: PriceRow[];
  /** By sort order then id. Always sent; optional only as the contract declares it. */
  packagings?: PackagingResponse[];
  /** How the variant's stock is tracked; always sent, optional only as the contract declares it. */
  tracking?: VariantTracking;
  /** The days a new lot's expiry and best-before lie after its production or receipt. */
  expiryDays?: number | null;
  bestBeforeDays?: number | null;
  createdAt: string;
  updatedAt: string;
}
export type VariantTracking = "none" | "lot" | "serial";
export const PACKAGING_KINDS = ["inner", "case", "pallet", "other"] as const;
export type PackagingKind = (typeof PACKAGING_KINDS)[number];
/** A box, case or pallet of a variant. quantity is in the variant's unit, derived when containsPackagingId is set. */
export interface PackagingResponse {
  id: number;
  name: string;
  kind: PackagingKind;
  containsPackagingId?: number | null;
  containsCount?: number | null;
  quantity: number;
  barcode?: string | null;
  weightKg?: number | null;
  lengthCm?: number | null;
  widthCm?: number | null;
  heightCm?: number | null;
  sortOrder: number;
  createdAt: string;
  updatedAt: string;
}
/** Exactly one of quantity, or containsPackagingId with containsCount. */
export interface PackagingInput {
  name: string;
  kind: PackagingKind;
  quantity?: number;
  containsPackagingId?: number;
  containsCount?: number;
  barcode?: string;
  weightKg?: number;
  lengthCm?: number;
  widthCm?: number;
  heightCm?: number;
  sortOrder?: number;
}
export interface VariantInput {
  sku: string;
  barcode?: string;
  unit?: string;
  standardCost?: number;
  weightKg?: number;
  lengthCm?: number;
  widthCm?: number;
  heightCm?: number;
  optionValues?: Record<string, string>;
  prices?: PriceInput[];
  /** An update that leaves it out keeps the stored mode. */
  tracking?: VariantTracking;
  /** An update that leaves them out clears them, as every field of the replacement. */
  expiryDays?: number;
  bestBeforeDays?: number;
}
export interface ProductResponse {
  id: number;
  name: string;
  sku?: string | null;
  type: ProductType;
  status: ProductStatus;
  unit?: string | null;
  standardCost?: number | null;
  taxCategory: TaxCategoryRef;
  description?: string | null;
  category?: ProductCategoryRef | null;
  barcode?: string | null;
  weightKg?: number | null;
  lengthCm?: number | null;
  widthCm?: number | null;
  heightCm?: number | null;
  effectivePrices: PriceRow[];
  variants: VariantResponse[];
  createdAt: string;
  updatedAt: string;
}
export interface PaginationMetadata {
  page: number;
  pageSize: number;
  totalCount: number;
  totalPages: number;
  hasNextPage: boolean;
  hasPreviousPage: boolean;
}
export interface PaginatedResponse<T> {
  data: T[];
  pagination: PaginationMetadata;
}
export interface ProductsQueryParams {
  page?: number;
  pageSize?: number;
  search?: string;
  sortBy?: "id" | "name" | "sku";
  sortDirection?: "asc" | "desc";
  status?: ProductStatus;
  categoryId?: number;
  uncategorized?: boolean;
}
export interface ProductInput {
  name: string;
  type: ProductType;
  status?: ProductStatus;
  taxCategoryId: number;
  description?: string;
  categoryId?: number;
  variants?: VariantInput[];
}
const queryString = (params: ProductsQueryParams) => {
  const searchParams = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== "") searchParams.set(key, String(value));
  return searchParams.size ? `?${searchParams}` : "";
};
export const productsQueryOptions = (params: ProductsQueryParams) =>
  queryOptions({
    queryKey: ["products", params],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<ProductResponse>>(`/api/v1/products${queryString(params)}`, { signal }),
    placeholderData: keepPreviousData,
  });
const fetchProduct = async (id: number, signal?: AbortSignal) => {
  try {
    return await request<ProductResponse>(`/api/v1/products/${id}`, { signal });
  } catch (error) {
    if ((error as { status?: number }).status === 404) throw new NotFoundError(`Product ${id} does not exist`);
    throw error;
  }
};
export const productQueryOptions = (id: number) =>
  queryOptions({ queryKey: ["products", id], queryFn: ({ signal }) => fetchProduct(id, signal) });
export const createProduct = (input: ProductInput) =>
  request<ProductResponse>("/api/v1/products", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const updateProduct = (id: number, input: Omit<ProductInput, "variants">) =>
  request<ProductResponse>(`/api/v1/products/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const archiveProduct = (id: number) => request<void>(`/api/v1/products/${id}`, { method: "DELETE" });
export const variantsQueryOptions = (productId: number) =>
  queryOptions({
    queryKey: ["products", productId, "variants"],
    queryFn: ({ signal }) => request<VariantResponse[]>(`/api/v1/products/${productId}/variants`, { signal }),
  });
export const addProductVariant = (productId: number, input: VariantInput) =>
  request<VariantResponse>(`/api/v1/products/${productId}/variants`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const updateProductVariant = (productId: number, variantId: number, input: VariantInput) =>
  request<VariantResponse>(`/api/v1/products/${productId}/variants/${variantId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const deleteProductVariant = (productId: number, variantId: number) =>
  request<void>(`/api/v1/products/${productId}/variants/${variantId}`, { method: "DELETE" });
export const productPricesQueryOptions = (productId: number, variantId: number) =>
  queryOptions({
    queryKey: ["products", productId, "variants", variantId, "prices"],
    queryFn: ({ signal }) =>
      request<PriceRow[]>(`/api/v1/products/${productId}/variants/${variantId}/prices`, { signal }),
  });
export const listProductPrices = (productId: number, variantId: number, signal?: AbortSignal) =>
  request<PriceRow[]>(`/api/v1/products/${productId}/variants/${variantId}/prices`, { signal });
export const addProductPrice = (productId: number, variantId: number, input: PriceInput) =>
  request<PriceRow>(`/api/v1/products/${productId}/variants/${variantId}/prices`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const updateProductPrice = (productId: number, variantId: number, priceId: number, input: PriceInput) =>
  request<PriceRow>(`/api/v1/products/${productId}/variants/${variantId}/prices/${priceId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const deleteProductPrice = (productId: number, variantId: number, priceId: number) =>
  request<void>(`/api/v1/products/${productId}/variants/${variantId}/prices/${priceId}`, { method: "DELETE" });
const packagingsPath = (productId: number, variantId: number) =>
  `/api/v1/products/${productId}/variants/${variantId}/packagings`;
export const addPackaging = (productId: number, variantId: number, input: PackagingInput) =>
  request<PackagingResponse>(packagingsPath(productId, variantId), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const updatePackaging = (productId: number, variantId: number, packagingId: number, input: PackagingInput) =>
  request<PackagingResponse>(`${packagingsPath(productId, variantId)}/${packagingId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const deletePackaging = (productId: number, variantId: number, packagingId: number) =>
  request<void>(`${packagingsPath(productId, variantId)}/${packagingId}`, { method: "DELETE" });

export const PRODUCT_IMAGE_USES = ["communications", "marketing"] as const;
export type ProductImageUse = (typeof PRODUCT_IMAGE_USES)[number];
/** An image of a product (messenger design N13): the normalised file, upright and without metadata. */
export interface ProductImage {
  id: number;
  productId: number;
  /** The server omits an empty alt text rather than sending null. */
  altText?: string | null;
  contentType: string;
  byteSize: number;
  width: number;
  height: number;
  /** The place among the product's images, from 0 with no gaps. */
  sortOrder: number;
  isPrimary: boolean;
  uses: ProductImageUse[];
  createdAt: string;
}
/** Every field is optional, and one left out is left as it is. An empty altText clears it. */
export interface ProductImageUpdate {
  altText?: string;
  uses?: ProductImageUse[];
  /** The place to move the image to; the others close the gap. */
  sortOrder?: number;
  /** Only true is allowed for the primary image's neighbours: another image's true is what unmarks it. */
  isPrimary?: boolean;
}
/** The key an image write invalidates: the section reads the product's list. */
export const productImagesQueryKey = (productId: number) => ["products", productId, "images"] as const;
export const productImagesQueryOptions = (productId: number) =>
  queryOptions({
    queryKey: productImagesQueryKey(productId),
    queryFn: ({ signal }) => request<ProductImage[]>(`/api/v1/products/${productId}/images`, { signal }),
  });
/**
 * Where an image's bytes are streamed from, for an img element; `thumb` is the
 * 320 px thumbnail. Images are never replaced in place, so the address is stable.
 */
export const productImageUrl = (productId: number, imageId: number, size?: "thumb"): string =>
  appUrl(`/api/v1/products/${productId}/images/${imageId}/content${size ? `?size=${size}` : ""}`);
/**
 * Sends one file: a part named `file`, the `Content-Type` left to the browser
 * so the multipart boundary is its own. Refused 422 with `code`
 * image_type_unsupported, image_too_large, image_too_small or image_invalid,
 * 503 storage_unavailable when the store cannot be written.
 */
export const uploadProductImage = (productId: number, file: File): Promise<ProductImage> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<ProductImage>(`/api/v1/products/${productId}/images`, { method: "POST", body: form });
};
export const updateProductImage = (productId: number, imageId: number, input: ProductImageUpdate) =>
  request<ProductImage>(`/api/v1/products/${productId}/images/${imageId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const deleteProductImage = (productId: number, imageId: number) =>
  request<void>(`/api/v1/products/${productId}/images/${imageId}`, { method: "DELETE" });
