import { keepPreviousData, queryOptions } from "@tanstack/react-query";

import type { PaginatedResponse } from "./customers";
import { NotFoundError, request } from "./request";

export interface ContactResponse {
  id: number;
  firstName: string;
  lastName: string;
  middleName: string | null;
  prefix: string | null;
  suffix: string | null;
  phone: string | null;
  email: string | null;
}

export interface ContactListItem {
  contact: ContactResponse;
  /** The number of customers the contact is associated with. */
  customerCount: number;
  /** The associated customer, when the contact is associated with exactly one. */
  customer: { id: number; name: string } | null;
}

export interface ContactInput {
  firstName: string;
  lastName: string;
  middleName?: string;
  prefix?: string;
  suffix?: string;
  phone?: string;
  email?: string;
}

/**
 * The typed role vocabulary (typed contact roles design D2). The server
 * validates against exactly these three, so a checkbox group can be built from
 * this array and a role that arrives outside it is still rendered (see
 * `contactRoleLabel`) rather than dropped.
 */
export const CONTACT_ROLES = ["billing", "project", "decision_maker"] as const;

/** One role a contact holds for a customer, and whether it is the primary holder. */
export interface ContactRoleAssignment {
  role: string;
  primary: boolean;
}

/** One role to give — `primary` omitted means false. */
export interface ContactRoleInput {
  role: string;
  primary?: boolean;
}

type RawContactRole = { role: string; primary?: boolean };

/**
 * `roles` is always sent by the server and `primary` is always present in it,
 * but the contract keeps both optional because the recorded exchange corpus
 * predates them — so absent and null mean "none", and a missing `primary` means
 * false. This is the one place that is decided, the same treatment `color` gets
 * in `tags.ts`.
 *
 * The order is restored rather than trusted: the API answers billing, project,
 * decision_maker, and re-sorting here means a component never has to know
 * whether what it holds came from a response, a cache or a future server.
 */
export const normalizeContactRoles = (raw?: RawContactRole[] | null): ContactRoleAssignment[] =>
  (raw ?? [])
    .map((r) => ({ role: r.role, primary: r.primary ?? false }))
    .sort((a, b) => roleRank(a.role) - roleRank(b.role));

const roleRank = (role: string) => {
  const index = (CONTACT_ROLES as readonly string[]).indexOf(role);
  return index === -1 ? CONTACT_ROLES.length : index;
};

export interface CustomerContactResponse {
  contact: ContactResponse;
  /** What this person is called at this customer, or null when the association has no title. */
  title: string | null;
  /** Every typed role held for this customer, in the fixed order. */
  roles: ContactRoleAssignment[];
  /** Connection-specific phone, when it differs from the contact's own. */
  phone: string | null;
  /** Connection-specific email, when it differs from the contact's own. */
  email: string | null;
}

export interface CustomerContactInput {
  title?: string;
  /** The complete set of roles to hold. Omitted leaves them unchanged. */
  roles?: ContactRoleInput[];
  phone?: string;
  email?: string;
}

export interface ContactCustomerResponse {
  customer: { id: number; name: string };
  title: string | null;
  roles: ContactRoleAssignment[];
  /** Connection-specific phone, when it differs from the contact's own. */
  phone: string | null;
  /** Connection-specific email, when it differs from the contact's own. */
  email: string | null;
}

/**
 * The wire shapes. Both fields the normalisers touch are optional here for the
 * same reason: the server omits what is unset rather than sending null, so an
 * association with no title has no `title` key at all, and `roles` is optional
 * in the contract (the recorded corpus predates it) even though the server
 * always sends it. Turning both absences into the one value the UI reads —
 * `null` and `[]` — is the boundary's job, not a redundancy.
 */
type RawCustomerContactResponse = Omit<CustomerContactResponse, "title" | "roles"> & {
  title?: string | null;
  roles?: RawContactRole[] | null;
};
type RawContactCustomerResponse = Omit<ContactCustomerResponse, "title" | "roles"> & {
  title?: string | null;
  roles?: RawContactRole[] | null;
};

export const normalizeCustomerContact = (raw: RawCustomerContactResponse): CustomerContactResponse => ({
  ...raw,
  title: raw.title ?? null,
  roles: normalizeContactRoles(raw.roles),
});

export const normalizeContactCustomer = (raw: RawContactCustomerResponse): ContactCustomerResponse => ({
  ...raw,
  title: raw.title ?? null,
  roles: normalizeContactRoles(raw.roles),
});

export interface ContactsQueryParams {
  page?: number;
  pageSize?: number;
  search?: string;
  sortBy?: "id" | "name";
  sortDirection?: "asc" | "desc";
}

/**
 * Performs an API request with the shared error handling: 400 validation problems
 * become ApiValidationError, 404 becomes NotFoundError, everything else a plain Error.
 */
const jsonBody = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const contactsQueryOptions = (params: ContactsQueryParams) =>
  queryOptions({
    queryKey: ["contacts", params],
    queryFn: ({ signal }) => {
      const searchParams = new URLSearchParams();
      if (params.page) searchParams.set("page", String(params.page));
      if (params.pageSize) searchParams.set("pageSize", String(params.pageSize));
      if (params.search) searchParams.set("search", params.search);
      if (params.sortBy) searchParams.set("sortBy", params.sortBy);
      if (params.sortDirection) searchParams.set("sortDirection", params.sortDirection);

      const query = searchParams.size > 0 ? `?${searchParams}` : "";
      return request<PaginatedResponse<ContactListItem>>(`/api/v1/customers/contacts${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });

export const customerContactsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "contacts"],
    queryFn: async ({ signal }) => {
      const answered = await request<{ data: RawCustomerContactResponse[] }>(
        `/api/v1/customers/${customerId}/contacts`,
        { signal },
      );
      return { data: answered.data.map(normalizeCustomerContact) };
    },
  });

export const contactQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["contacts", id],
    queryFn: async ({ signal }) => {
      try {
        return await request<ContactResponse>(`/api/v1/customers/contacts/${id}`, { signal });
      } catch (error) {
        if ((error as { status?: number }).status === 404) throw new NotFoundError(`Contact ${id} does not exist`);
        throw error;
      }
    },
  });

export const contactCustomersQueryOptions = (contactId: number) =>
  queryOptions({
    queryKey: ["contacts", contactId, "customers"],
    queryFn: async ({ signal }) => {
      const answered = await request<{ data: RawContactCustomerResponse[] }>(
        `/api/v1/customers/contacts/${contactId}/customers`,
        { signal },
      );
      return { data: answered.data.map(normalizeContactCustomer) };
    },
  });

export const createContact = (input: ContactInput) =>
  request<ContactResponse>("/api/v1/customers/contacts", jsonBody("POST", input));

export const updateContact = (id: number, input: ContactInput) =>
  request<ContactResponse>(`/api/v1/customers/contacts/${id}`, jsonBody("PUT", input));

export const deleteContact = (id: number) => request<void>(`/api/v1/customers/contacts/${id}`, { method: "DELETE" });

export const attachCustomerContact = async (customerId: number, input: CustomerContactInput & { contactId: number }) =>
  normalizeCustomerContact(
    await request<RawCustomerContactResponse>(`/api/v1/customers/${customerId}/contacts`, jsonBody("POST", input)),
  );

export const updateCustomerContact = (customerId: number, contactId: number, input: CustomerContactInput) =>
  request<RawCustomerContactResponse>(`/api/v1/customers/${customerId}/contacts/${contactId}`, jsonBody("PUT", input))
    .then(normalizeCustomerContact)
    .catch((error: unknown) => {
      if ((error as { status?: number }).status === 404)
        throw new NotFoundError("The requested contact association does not exist");
      throw error;
    });

export const detachCustomerContact = (customerId: number, contactId: number) =>
  request<void>(`/api/v1/customers/${customerId}/contacts/${contactId}`, { method: "DELETE" });
