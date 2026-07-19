import type { Contact } from "@vantigo/customers/database/schema/contacts";
import { request } from "@vantigo/customers/lib/customers/api-client";
import type { ContactListResult, ContactWithCustomers, CustomerContactItem } from "./queries";
import type {
  ContactCreateInput,
  ContactListQuery,
  ContactUpdateInput,
  CustomerContactCreateInput,
  CustomerContactUpdateInput,
} from "./schemas";

export type { Contact, ContactListResult, ContactWithCustomers, CustomerContactItem };

export function fetchContacts(query: Partial<ContactListQuery>): Promise<ContactListResult> {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  return request(`/api/contacts?${params}`);
}

export async function fetchContact(id: number): Promise<ContactWithCustomers> {
  const { contact } = await request<{ contact: ContactWithCustomers }>(`/api/contacts/${id}`);
  return contact;
}

export function createContact(input: ContactCreateInput): Promise<{ contact: Contact }> {
  return request("/api/contacts", { method: "POST", body: JSON.stringify(input) });
}

export async function updateContact(id: number, input: ContactUpdateInput): Promise<Contact> {
  const { contact } = await request<{ contact: Contact }>(`/api/contacts/${id}`, {
    method: "PATCH",
    body: JSON.stringify(input),
  });
  return contact;
}

export function deleteContact(id: number): Promise<{ ok: boolean }> {
  return request(`/api/contacts/${id}`, { method: "DELETE" });
}

export async function fetchCustomerContacts(customerId: number): Promise<CustomerContactItem[]> {
  const { contacts } = await request<{ contacts: CustomerContactItem[] }>(
    `/api/customers/${customerId}/contacts`,
  );
  return contacts;
}

export function addCustomerContact(
  customerId: number,
  input: CustomerContactCreateInput,
): Promise<{ contactId: number }> {
  return request(`/api/customers/${customerId}/contacts`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function updateCustomerContact(
  customerId: number,
  contactId: number,
  input: CustomerContactUpdateInput,
): Promise<{ ok: boolean }> {
  return request(`/api/customers/${customerId}/contacts/${contactId}`, {
    method: "PATCH",
    body: JSON.stringify(input),
  });
}

export function unlinkCustomerContact(
  customerId: number,
  contactId: number,
): Promise<{ ok: boolean }> {
  return request(`/api/customers/${customerId}/contacts/${contactId}`, { method: "DELETE" });
}
