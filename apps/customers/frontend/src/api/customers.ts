import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { ApiConflictError } from "./request";
import { NotFoundError, request } from "./request";

export { ApiConflictError, ApiValidationError, NotFoundError } from "./request";

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

/**
 * A customer's own contact details (invoice-ready customer design D2) — what
 * reaches the customer itself, not one of its contacts. Each field is
 * nullable; a blank value is stored (and shown) as null.
 */
export interface CustomerContactInfo {
  email: string | null;
  phone: string | null;
  website: string | null;
}

export interface CustomerResponse {
  id: number;
  /** The customer-facing number (KVEM1000-CU style), shown in the list in place of the database id. */
  customerNumber: number;
  name: string;
  status: string;
  type: CustomerType;
  createdAt: string;
  updatedAt: string;
  /** Null when the customer has no legal identity or the caller lacks permission to view it. */
  identity: CustomerIdentitySummary | null;
  timelineSummary: { entryCount: number; latestOccurredOn: string | null };
  /** The row's optimistic-concurrency token (design D5). Absent only for corpus responses that predate it. */
  revision?: number;
  /** Design D2. Always present on responses from this version on; absent only for corpus responses that predate it. */
  contactInfo?: CustomerContactInfo;
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

/** Naming a status shows exactly that status: `archived` needs no separate "include archived" flag. */
export type CustomerStatusFilter = "active" | "disabled" | "archived";

export interface CustomersQueryParams {
  page?: number;
  pageSize?: number;
  search?: string;
  status?: CustomerStatusFilter;
  type?: CustomerType;
  sortBy?: "id" | "name" | "customerNumber" | "createdAt" | "updatedAt";
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
  if (params.status) searchParams.set("status", params.status);
  if (params.type) searchParams.set("type", params.type);
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

/** The page's own view of its search-derived state: what the list URL carries. */
export interface CustomersListSearch {
  page: number;
  search: string;
  status?: CustomerStatusFilter;
  type?: CustomerType;
  sortBy?: CustomersQueryParams["sortBy"];
  sortDirection?: CustomersQueryParams["sortDirection"];
}

export const CUSTOMERS_PAGE_SIZE = 25;

/**
 * Turns the list page's URL search state into `CustomersQueryParams`, the one
 * place that mapping happens. The host route's loader and the page component
 * both call this on the same search value, so their query keys always match
 * and the loader's prefetch is never wasted on a second fetch.
 */
export const customersListParams = (search: CustomersListSearch): CustomersQueryParams => ({
  page: search.page,
  pageSize: CUSTOMERS_PAGE_SIZE,
  search: search.search || undefined,
  status: search.status,
  type: search.type,
  sortBy: search.sortBy,
  sortDirection: search.sortDirection,
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
  /** The row's revision, so a write against a stale read is refused rather than silently overwriting a concurrent change (design D5). Omitted when the caller has no revision to compare (create). */
  revision?: number;
  /** Goes ahead even though the identity is already used by another customer (design D6). */
  allowDuplicateIdentity?: boolean;
}

/** One customer already holding the legal identity a write tried to set (design D6). */
export interface ConflictDuplicate {
  id: number;
  customerNumber: number;
  name: string;
  status: string;
}

const isConflictDuplicate = (value: unknown): value is ConflictDuplicate =>
  !!value &&
  typeof value === "object" &&
  typeof (value as Record<string, unknown>).id === "number" &&
  typeof (value as Record<string, unknown>).customerNumber === "number" &&
  typeof (value as Record<string, unknown>).name === "string" &&
  typeof (value as Record<string, unknown>).status === "string";

/**
 * Reads D6's duplicate list out of a duplicate-legal-identity `ApiConflictError`.
 * `duplicates` is this module's own conflict field, not one `ApiConflictError`
 * types generically (its `problem` is a plain `Record<string, unknown>`,
 * shared across every app that reuses the class) — so this module validates
 * the shape itself rather than trusting the server's JSON blindly. Anything
 * that does not match is dropped rather than shown as a broken row.
 */
export const conflictDuplicates = (error: ApiConflictError): ConflictDuplicate[] => {
  const raw = error.problem.duplicates;
  return Array.isArray(raw) ? raw.filter(isConflictDuplicate) : [];
};

/**
 * The subset of contact info the create form offers (email and phone —
 * design D6). Website is part of the contract's `contactInfo` too, but
 * nothing on create collects it; the type stays narrow to what callers
 * actually send.
 */
export interface CreateContactInfoInput {
  email?: string;
  phone?: string;
}

/** The type is chosen on create only; afterwards it changes through changeCustomerType. */
export interface CreateCustomerInput extends CustomerInput {
  type?: CustomerType;
  /** Design D2: a customer can be created contact-ready in one call. */
  contactInfo?: CreateContactInfoInput;
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
export async function changeCustomerType(id: number, type: CustomerType, revision?: number): Promise<CustomerResponse> {
  return request<CustomerResponse>(`/api/v1/customers/${id}/type`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type, revision }),
  });
}

/** Archives a customer (design D7). Idempotent, like the endpoint itself. */
export const archiveCustomer = (id: number) => request<void>(`/api/v1/customers/${id}`, { method: "DELETE" });

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
/**
 * The dedicated legal-identity PUT's body. It is the five identity fields at
 * the top level, plus D6's override — which lives here rather than on
 * `LegalIdentityInput` because that type is also the nested `identity` of
 * create/update, where the flag belongs to the *enclosing* request
 * (`CustomerInput.allowDuplicateIdentity`) and would be inert if nested.
 */
export interface LegalIdentityUpsertInput extends LegalIdentityInput {
  /** Goes ahead even though the identity is already used by another customer (design D6). */
  allowDuplicateIdentity?: boolean;
}

export const upsertLegalIdentity = (id: number, input: LegalIdentityUpsertInput) =>
  request<LegalIdentityResponse>(`/api/v1/customers/${id}/legal-identity`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const deleteLegalIdentity = (id: number) =>
  request<void>(`/api/v1/customers/${id}/legal-identity`, { method: "DELETE" });

/**
 * PUT /customers/{id}/contact-info's own request body (design D1, D2): a full
 * replace of the customer's contact info — every field present or null,
 * absent and null both meaning the field is cleared. `revision` is optional,
 * as `updateCustomer`'s own is.
 */
export interface ContactInfoInput {
  email: string | null;
  phone: string | null;
  website: string | null;
  revision?: number;
}

/**
 * Replaces a customer's contact info. Answers the full customer (not just the
 * contact info) so a caller can read the fresh revision straight off the
 * response, the way `updateCustomer` does.
 */
export const updateContactInfo = (id: number, input: ContactInfoInput) =>
  request<CustomerResponse>(`/api/v1/customers/${id}/contact-info`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
