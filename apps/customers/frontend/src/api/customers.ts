import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { NotFoundError, request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";

export interface LegalIdentityResponse {
  country: string;
  type: string;
  id: string;
  name: string;
  source: string;
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
  return request(`/api/v1/customers${query}`, { signal });
}

export const customersQueryOptions = (params: CustomersQueryParams) =>
  queryOptions({
    queryKey: ["customers", params],
    queryFn: ({ signal }) => fetchCustomers(params, signal),
    placeholderData: keepPreviousData,
  });

/** The requested resource does not exist (HTTP 404). */
async function fetchCustomer(id: number, signal?: AbortSignal): Promise<CustomerResponse> {
  try {
    return await request(`/api/v1/customers/${id}`, { signal });
  } catch (error) {
    if ((error as { status?: number }).status === 404) throw new NotFoundError(`Customer ${id} does not exist`);
    throw error;
  }
}

export const customerQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["customers", id],
    queryFn: ({ signal }) => fetchCustomer(id, signal),
  });

/**
 * A 400 validation problem (RFC 9457) from the API, carrying errors keyed by the
 * camelCase JSON path of the offending request field (e.g. "name").
 */
export interface LegalIdentityInput {
  country: string;
  type: string;
  id: string;
  name: string;
  source: string;
}

export interface CustomerInput {
  name: string;
  identity?: LegalIdentityInput | null;
}

export async function createCustomer(input: CustomerInput): Promise<{ id: number }> {
  return request<{ id: number }>("/api/v1/customers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}

export async function updateCustomer(id: number, input: CustomerInput): Promise<CustomerResponse> {
  return request<CustomerResponse>(`/api/v1/customers/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
}
