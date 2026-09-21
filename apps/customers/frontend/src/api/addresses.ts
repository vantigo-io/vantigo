import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export { ApiConflictError, ApiValidationError, NotFoundError } from "./request";

/** The union D3 keeps — the same four every compared invoicing product uses. */
export type CustomerAddressType = "invoice" | "postal" | "delivery" | "visiting";

/** The fixed display order design D6 groups a customer's addresses in. */
export const ADDRESS_TYPE_ORDER: readonly CustomerAddressType[] = ["invoice", "postal", "delivery", "visiting"];

/** One of a customer's typed addresses (design D3). */
export interface CustomerAddress {
  id: number;
  type: string;
  label: string | null;
  line1: string;
  line2: string | null;
  postalCode: string | null;
  city: string | null;
  region: string | null;
  country: string;
  isPrimary: boolean;
  createdAt: string;
  updatedAt: string;
}

/**
 * POST/PUT .../addresses(/{addressId})'s own request body (design D1, D3): a
 * full replace of one address — every field present or null.
 * `isPrimary: undefined`/`false` follows D3's own rules server-side: the
 * first address of a type is primary whatever the request says, and `false`
 * on the address that is currently the only or primary one of its type is
 * refused (400, keyed `isPrimary`).
 */
export interface CustomerAddressInput {
  type: string;
  label: string | null;
  line1: string;
  line2: string | null;
  postalCode: string | null;
  city: string | null;
  region: string | null;
  country: string;
  isPrimary: boolean;
}

async function fetchCustomerAddresses(customerId: number, signal?: AbortSignal): Promise<CustomerAddress[]> {
  const response = await request<{ data: CustomerAddress[] }>(`/api/v1/customers/${customerId}/addresses`, { signal });
  return response.data;
}

export const customerAddressesQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "addresses"],
    queryFn: ({ signal }) => fetchCustomerAddresses(customerId, signal),
  });

export const createCustomerAddress = (customerId: number, input: CustomerAddressInput) =>
  request<CustomerAddress>(`/api/v1/customers/${customerId}/addresses`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateCustomerAddress = (customerId: number, addressId: number, input: CustomerAddressInput) =>
  request<CustomerAddress>(`/api/v1/customers/${customerId}/addresses/${addressId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/**
 * Address writes carry no revision of their own (design D3: they do not
 * touch the customer row) — "Make primary" is a full replace of the address
 * with `isPrimary: true`, everything else unchanged.
 */
export const makeAddressPrimary = (customerId: number, address: CustomerAddress) =>
  updateCustomerAddress(customerId, address.id, {
    type: address.type,
    label: address.label,
    line1: address.line1,
    line2: address.line2,
    postalCode: address.postalCode,
    city: address.city,
    region: address.region,
    country: address.country,
    isPrimary: true,
  });

export const deleteCustomerAddress = (customerId: number, addressId: number) =>
  request<void>(`/api/v1/customers/${customerId}/addresses/${addressId}`, { method: "DELETE" });
