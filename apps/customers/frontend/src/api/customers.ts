import { keepPreviousData, queryOptions } from "@tanstack/react-query";

export interface LegalIdentityResponse {
  country: string;
  type: string;
  id: string;
  name: string;
}

export interface CustomerResponse {
  id: number;
  name: string;
  identity: LegalIdentityResponse | null;
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

export interface CustomersQueryParams {
  page?: number;
  pageSize?: number;
  search?: string;
  sortBy?: "id" | "name";
  sortDirection?: "asc" | "desc";
}

async function fetchCustomers(
  params: CustomersQueryParams,
  signal: AbortSignal,
): Promise<PaginatedResponse<CustomerResponse>> {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.pageSize) searchParams.set("pageSize", String(params.pageSize));
  if (params.search) searchParams.set("search", params.search);
  if (params.sortBy) searchParams.set("sortBy", params.sortBy);
  if (params.sortDirection) searchParams.set("sortDirection", params.sortDirection);

  const query = searchParams.size > 0 ? `?${searchParams}` : "";
  const response = await fetch(`/api/v1/customers${query}`, { signal });

  if (!response.ok) {
    throw new Error(`Failed to fetch customers (HTTP ${response.status})`);
  }

  return response.json();
}

export const customersQueryOptions = (params: CustomersQueryParams) =>
  queryOptions({
    queryKey: ["customers", params],
    queryFn: ({ signal }) => fetchCustomers(params, signal),
    placeholderData: keepPreviousData,
  });
