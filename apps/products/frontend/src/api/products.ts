import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { NotFoundError, request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";
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
  createdAt: string;
  updatedAt: string;
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
