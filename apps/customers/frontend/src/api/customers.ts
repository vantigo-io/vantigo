import { hashKey, keepPreviousData, type QueryClient, type QueryKey, queryOptions } from "@tanstack/react-query";
import { type CustomerBillingProfile, customerBillingProfileQueryOptions } from "./billing-profile";
import type { ApiConflictError } from "./request";
import { NotFoundError, request } from "./request";
import { type CustomerTag, normalizeTag } from "./tags";

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
 * The user accountable for this customer relationship (owner and tags design
 * D1). `active` is false for an account that has been disabled since it was
 * assigned, and for one the directory no longer knows at all — which reads as
 * `displayName: "Unknown user"`. Either way the customer still has an owner:
 * nothing is revoked behind the caller's back.
 */
export interface CustomerOwner {
  userId: string;
  displayName: string;
  active: boolean;
}

/**
 * The group a customer belongs to (customer groups design D3) — at most one.
 * The group's own default payment term is not here: the billing card reads it
 * from the profile's `groupDefault`, already resolved, and the Manage groups
 * modal from the vocabulary list.
 */
export interface CustomerGroupRef {
  id: string;
  name: string;
}

/** The list's Owner filter: the caller's own customers, or the unassigned ones. */
export type CustomerOwnerFilter = "me" | "none";

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
  /** Design D1. Null when the customer is unowned; the wire omits the field entirely in that case. */
  owner: CustomerOwner | null;
  /** Design D3. Null when the customer belongs to no group; the wire omits the field entirely in that case. */
  group: CustomerGroupRef | null;
  /** Design D2. Always an array — the boundary turns an omitted field into `[]`. */
  tags: CustomerTag[];
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
  ownerId?: string;
  tagId?: string;
  groupId?: string;
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
  if (params.ownerId) searchParams.set("ownerId", params.ownerId);
  if (params.tagId) searchParams.set("tagId", params.tagId);
  if (params.groupId) searchParams.set("groupId", params.groupId);

  const query = searchParams.size > 0 ? `?${searchParams}` : "";
  const response = await request<PaginatedResponse<RawCustomerResponse>>(`/api/v1/customers${query}`, { signal });
  return { ...response, data: response.data.map(normalizeCustomer) };
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
  /**
   * The two literals and nothing else. The API would also take a user id, but
   * the list offers only 'Mine' and 'Unassigned' and the host's route validates
   * the URL down to exactly those (`OWNER_FILTERS`) — so the type says what can
   * actually arrive, and a page that tried to put a uuid here would not compile.
   */
  ownerId?: CustomerOwnerFilter;
  tagId?: string;
  groupId?: string;
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
  ownerId: search.ownerId,
  tagId: search.tagId,
  groupId: search.groupId,
});

export const customerStatsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "stats"],
    queryFn: ({ signal }) => request<CustomerStatsResponse>("/api/v1/customers/stats", { signal }),
  });

/**
 * A customer as it actually arrives: `contactInfo`'s three fields are
 * `omitempty` on the wire (see `apps/server/internal/customers/gen/api.gen.go`),
 * so a customer with only a phone number comes back without `email` and
 * `website` rather than with nulls. Absent and null mean the same thing
 * (design D2), so the boundary maps them to the one shape the rest of the
 * package reads — the same treatment addresses and the billing profile get.
 * `contactInfo` itself stays optional: it is genuinely absent on the
 * recorded responses that predate design D2.
 */
type RawCustomerResponse = Omit<CustomerResponse, "contactInfo" | "owner" | "group" | "tags"> & {
  contactInfo?: Partial<CustomerContactInfo>;
  owner?: CustomerOwner | null;
  group?: CustomerGroupRef | null;
  tags?: { id: string; name: string; color?: string | null }[];
};

export const normalizeCustomer = ({
  contactInfo,
  owner,
  group,
  tags,
  ...rest
}: RawCustomerResponse): CustomerResponse => {
  // The four normalised fields are destructured out, so what is left is
  // already the rest of a `CustomerResponse` and the two arms below need no
  // cast to say so — `contactInfo` is optional on the result, which is exactly
  // what "absent on responses that predate design D2" means.
  const normalized: CustomerResponse = {
    ...rest,
    owner: owner ?? null,
    group: group ?? null,
    tags: (tags ?? []).map(normalizeTag),
  };
  if (!contactInfo) return normalized;
  return {
    ...normalized,
    contactInfo: {
      email: contactInfo.email ?? null,
      phone: contactInfo.phone ?? null,
      website: contactInfo.website ?? null,
    },
  };
};

/** The requested resource does not exist (HTTP 404). */
async function fetchCustomer(id: number, signal?: AbortSignal): Promise<CustomerResponse> {
  try {
    return normalizeCustomer(await request<RawCustomerResponse>(`/api/v1/customers/${id}`, { signal }));
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
 * The customer row's revision (design D5) lives in two cache entries: the
 * customer itself and its billing profile, whose revision *is* the row's
 * (design D4). Six editors — the form modal, the type change, contact info,
 * the billing profile, the owner PUT (owner and tags design D1, the owner
 * being a column on the row) and the group PUT (customer groups design D3, for
 * the same reason) — plus Restore all send it, so a write that gets a
 * fresh revision back writes it to both entries straight away, before its
 * own invalidations: those cost a round trip, and an editor opened inside
 * that window would otherwise seed itself from the revision the server has
 * already moved past and come back with a spurious 409.
 *
 * Entries that are not cached are left alone — a revision is not enough to
 * invent a row out of — and a write that answers no revision at all (Archive
 * is a 204) simply has nothing to sync.
 */
export const syncCustomerRevision = (queryClient: QueryClient, id: number, revision: number | undefined) => {
  if (revision === undefined) return;
  queryClient.setQueryData(customerQueryOptions(id).queryKey, (old?: CustomerResponse) =>
    old ? { ...old, revision } : old,
  );
  queryClient.setQueryData(customerBillingProfileQueryOptions(id).queryKey, (old?: CustomerBillingProfile) =>
    old ? { ...old, revision } : old,
  );
};

/**
 * Invalidates every `["customers"]` query except the one whose fresh data
 * the caller has just put in the cache itself — from a write's own 200 body
 * or a `fetchQuery` a moment earlier. Without the exception the broad prefix
 * would throw that data away and fetch it again, which is the one round trip
 * reading the response body was meant to save.
 */
export const invalidateCustomersExcept = (queryClient: QueryClient, except: QueryKey) => {
  const alreadyFresh = hashKey(except);
  return queryClient.invalidateQueries({
    queryKey: ["customers"],
    predicate: (query) => hashKey(query.queryKey) !== alreadyFresh,
  });
};

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
  return normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );
}

/**
 * Sets whether the customer is a business or a private person. Deliberately not
 * part of updateCustomer: a legal identity of the old type is removed with it.
 */
export async function changeCustomerType(id: number, type: CustomerType, revision?: number): Promise<CustomerResponse> {
  return normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${id}/type`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ type, revision }),
    }),
  );
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
export const updateContactInfo = async (id: number, input: ContactInfoInput) =>
  normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${id}/contact-info`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );
