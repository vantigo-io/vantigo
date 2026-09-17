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

export interface CustomerIdentitySummary {
  country: string;
  type: string;
  id: string;
}

/** Whether a customer is a company or a private person, independent of its legal identity. */
export type CustomerType = "business" | "person";

export interface CustomerResponse {
  id: number;
  name: string;
  status: string;
  type: CustomerType;
  createdAt: string;
  updatedAt: string;
  /** Null when the customer has no legal identity or the caller lacks permission to view it. */
  identity: CustomerIdentitySummary | null;
  timelineSummary: { entryCount: number; latestOccurredOn: string | null };
}

export interface CustomerStatsResponse {
  totalCount: number;
  activeCount: number;
  newLast30DaysCount: number;
  /** Null when the caller lacks the legal-identity view permission. */
  businessCount: number | null;
  personCount: number | null;
  missingIdentityCount: number | null;
  distinctCountryCount: number | null;
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

export const customerStatsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "stats"],
    queryFn: ({ signal }) => request<CustomerStatsResponse>("/api/v1/customers/stats", { signal }),
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
  identity?: LegalIdentityInput;
  status?: string;
}

/** The type is chosen on create only; afterwards it changes through changeCustomerType. */
export interface CreateCustomerInput extends CustomerInput {
  type?: CustomerType;
}

export async function createCustomer(input: CreateCustomerInput): Promise<{ id: number }> {
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

/**
 * Sets whether the customer is a business or a private person. Deliberately not
 * part of updateCustomer: a legal identity of the old type is removed with it.
 */
export async function changeCustomerType(id: number, type: CustomerType): Promise<CustomerResponse> {
  return request<CustomerResponse>(`/api/v1/customers/${id}/type`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type }),
  });
}

export const legalIdentityQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["customers", id, "legal-identity"],
    queryFn: async ({ signal }) => {
      try {
        return (await request<LegalIdentityResponse>(`/api/v1/customers/${id}/legal-identity`, { signal })) ?? null;
      } catch (error) {
        if ([403, 404].includes((error as { status?: number }).status ?? 0)) return null;
        throw error;
      }
    },
  });
export const upsertLegalIdentity = (id: number, input: LegalIdentityInput) =>
  request<LegalIdentityResponse>(`/api/v1/customers/${id}/legal-identity`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const deleteLegalIdentity = (id: number) =>
  request<void>(`/api/v1/customers/${id}/legal-identity`, { method: "DELETE" });
