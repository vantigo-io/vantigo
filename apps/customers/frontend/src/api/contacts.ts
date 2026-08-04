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

export interface CustomerContactResponse {
  contact: ContactResponse;
  role: string;
  /** Connection-specific phone, when it differs from the contact's own. */
  phone: string | null;
  /** Connection-specific email, when it differs from the contact's own. */
  email: string | null;
}

export interface CustomerContactInput {
  role: string;
  phone?: string;
  email?: string;
}

export interface ContactCustomerResponse {
  customer: { id: number; name: string };
  role: string;
  /** Connection-specific phone, when it differs from the contact's own. */
  phone: string | null;
  /** Connection-specific email, when it differs from the contact's own. */
  email: string | null;
}

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
      return request<PaginatedResponse<ContactListItem>>(`/api/v1/contacts${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });

export const customerContactsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "contacts"],
    queryFn: ({ signal }) =>
      request<{ data: CustomerContactResponse[] }>(`/api/v1/customers/${customerId}/contacts`, {
        signal,
      }),
  });

export const contactQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["contacts", id],
    queryFn: async ({ signal }) => {
      try {
        return await request<ContactResponse>(`/api/v1/contacts/${id}`, { signal });
      } catch (error) {
        if ((error as { status?: number }).status === 404) throw new NotFoundError(`Contact ${id} does not exist`);
        throw error;
      }
    },
  });

export const contactCustomersQueryOptions = (contactId: number) =>
  queryOptions({
    queryKey: ["contacts", contactId, "customers"],
    queryFn: ({ signal }) =>
      request<{ data: ContactCustomerResponse[] }>(`/api/v1/contacts/${contactId}/customers`, {
        signal,
      }),
  });

export const createContact = (input: ContactInput) =>
  request<ContactResponse>("/api/v1/contacts", jsonBody("POST", input));

export const updateContact = (id: number, input: ContactInput) =>
  request<ContactResponse>(`/api/v1/contacts/${id}`, jsonBody("PUT", input));

export const deleteContact = (id: number) => request<void>(`/api/v1/contacts/${id}`, { method: "DELETE" });

export const attachCustomerContact = (customerId: number, input: CustomerContactInput & { contactId: number }) =>
  request<CustomerContactResponse>(`/api/v1/customers/${customerId}/contacts`, jsonBody("POST", input));

export const updateCustomerContact = (customerId: number, contactId: number, input: CustomerContactInput) =>
  request<CustomerContactResponse>(
    `/api/v1/customers/${customerId}/contacts/${contactId}`,
    jsonBody("PUT", input),
  ).catch((error: unknown) => {
    if ((error as { status?: number }).status === 404)
      throw new NotFoundError("The requested contact association does not exist");
    throw error;
  });

export const detachCustomerContact = (customerId: number, contactId: number) =>
  request<void>(`/api/v1/customers/${customerId}/contacts/${contactId}`, { method: "DELETE" });
