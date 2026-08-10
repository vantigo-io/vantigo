import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { NotFoundError, request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";

export type ProductType = "Goods" | "Service";
export type ProductStatus = "Draft" | "Active" | "Discontinued";

export interface PriceRow {
  id: number;
  currency: string;
  amount: number;
  validFrom: string | null;
  validTo: string | null;
}

export interface ProductCategoryRef {
  id: number;
  name: string;
}

export interface ProductResponse {
  id: number;
  name: string;
  sku: string;
  type: ProductType;
  status: ProductStatus;
  unit: string;
  standardCost: number | null;
  vatRate: number;
  description: string | null;
  category: ProductCategoryRef | null;
  barcode: string | null;
  weightKg: number | null;
  lengthCm: number | null;
  widthCm: number | null;
  heightCm: number | null;
  effectivePrices: PriceRow[];
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
  /** Only products without a category. Cannot be combined with categoryId. */
  uncategorized?: boolean;
}

export interface ProductInput {
  name: string;
  sku: string;
  type: ProductType;
  status?: ProductStatus;
  unit?: string;
  standardCost?: number;
  vatRate: number;
  description?: string;
  categoryId?: number;
  barcode?: string;
  weightKg?: number;
  lengthCm?: number;
  widthCm?: number;
  heightCm?: number;
  prices?: PriceInput[];
}

export interface PriceInput {
  currency: string;
  amount: number;
  validFrom?: string;
  validTo?: string;
}

const queryString = (params: ProductsQueryParams) => {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.pageSize) searchParams.set("pageSize", String(params.pageSize));
  if (params.sortBy) searchParams.set("sortBy", params.sortBy);
  if (params.sortDirection) searchParams.set("sortDirection", params.sortDirection);
  if (params.search) searchParams.set("search", params.search);
  if (params.status) searchParams.set("status", params.status);
  if (params.categoryId) searchParams.set("categoryId", String(params.categoryId));
  if (params.uncategorized) searchParams.set("uncategorized", "true");
  return searchParams.size > 0 ? `?${searchParams}` : "";
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
  queryOptions({
    queryKey: ["products", id],
    queryFn: ({ signal }) => fetchProduct(id, signal),
  });

export const createProduct = (input: ProductInput) =>
  request<ProductResponse>("/api/v1/products", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateProduct = (id: number, input: ProductInput) =>
  request<ProductResponse>(`/api/v1/products/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const archiveProduct = (id: number) => request<void>(`/api/v1/products/${id}`, { method: "DELETE" });

export const productPricesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["products", id, "prices"],
    queryFn: ({ signal }) => request<PriceRow[]>(`/api/v1/products/${id}/prices`, { signal }),
  });

export const listProductPrices = (id: number, signal?: AbortSignal) =>
  request<PriceRow[]>(`/api/v1/products/${id}/prices`, { signal });

export const addProductPrice = (id: number, input: PriceInput) =>
  request<PriceRow>(`/api/v1/products/${id}/prices`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateProductPrice = (id: number, priceId: number, input: PriceInput) =>
  request<PriceRow>(`/api/v1/products/${id}/prices/${priceId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const deleteProductPrice = (id: number, priceId: number) =>
  request<void>(`/api/v1/products/${id}/prices/${priceId}`, { method: "DELETE" });
