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

/**
 * A 400 validation problem (RFC 9457) from the API, carrying errors keyed by the
 * camelCase JSON path of the offending request field (e.g. "name").
 */
export class ApiValidationError extends Error {
  readonly errors: Record<string, string[]>;

  constructor(title: string, errors: Record<string, string[]>) {
    super(title);
    this.name = "ApiValidationError";
    this.errors = errors;
  }

  /** The first error message per field, suitable for `form.setErrors`. */
  get fieldErrors(): Record<string, string> {
    return Object.fromEntries(Object.entries(this.errors).map(([field, messages]) => [field, messages[0]]));
  }
}

async function throwApiError(response: Response): Promise<never> {
  if (response.status === 400) {
    const problem = await response.json().catch(() => null);
    if (problem && typeof problem === "object" && "errors" in problem) {
      throw new ApiValidationError(problem.title ?? "Validation failed", problem.errors);
    }
  }

  throw new Error(`Request failed (HTTP ${response.status})`);
}

export interface LegalIdentityInput {
  country: string;
  type: string;
  id: string;
  name: string;
}

export interface CustomerInput {
  name: string;
  identity?: LegalIdentityInput | null;
}

export async function createCustomer(input: CustomerInput): Promise<{ id: number }> {
  const response = await fetch("/api/v1/customers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

  if (!response.ok) {
    await throwApiError(response);
  }

  return response.json();
}

export async function updateCustomer(id: number, input: CustomerInput): Promise<CustomerResponse> {
  const response = await fetch(`/api/v1/customers/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

  if (!response.ok) {
    await throwApiError(response);
  }

  return response.json();
}
