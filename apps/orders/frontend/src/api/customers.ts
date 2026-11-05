import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";

/**
 * What this package reads of the customers module, over HTTP (module
 * boundaries rule 7): the picker's search, and a customer's addresses and
 * contacts for the delivery address and contact pickers (O13). The shapes are
 * typed here rather than generated: this package owns only the orders
 * contract, and these are the fields it reads.
 */
export interface CustomerOption {
  id: number;
  name: string;
  customerNumber: number;
  status: string;
}

/**
 * A search of the customers (`customers:view`). A new document's customer is
 * picked among the active ones; the lists' filter offers every customer
 * (`anyStatus`), since an archived customer's documents are still found by it.
 */
export const customerSearchQueryOptions = (search: string, anyStatus = false) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "customers", search, anyStatus],
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ pageSize: "20" });
      if (anyStatus) params.set("includeArchived", "true");
      else params.set("status", "active");
      if (search.trim()) params.set("search", search.trim());
      const answer = await request<{ data: CustomerOption[] }>(`/api/v1/customers?${params.toString()}`, { signal });
      return answer.data;
    },
  });

/** One of a customer's addresses as the customers module lists it. */
export interface CustomerAddressOption {
  id: number;
  type: string;
  isPrimary: boolean;
  label?: string | null;
  line1: string;
  line2?: string | null;
  postalCode?: string | null;
  city?: string | null;
  country: string;
}

/**
 * The addresses a delivery address may be chosen among: the customer's
 * delivery addresses first, then the rest (`customers:view`). The server
 * snapshots the one chosen on the document (O13).
 */
export const customerAddressesQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "customer-addresses", customerId],
    retry: false,
    queryFn: async ({ signal }) => {
      const answer = await request<{ data: CustomerAddressOption[] }>(`/api/v1/customers/${customerId}/addresses`, {
        signal,
      });
      const rank = (a: CustomerAddressOption) => (a.type === "delivery" ? 0 : 1) * 2 + (a.isPrimary ? 0 : 1);
      return [...answer.data].sort((a, b) => rank(a) - rank(b));
    },
  });

/** One of a customer's contacts as the customers module lists them. */
export interface CustomerContactOption {
  contact: { id: number; firstName: string; lastName: string; email?: string | null; phone?: string | null };
  email?: string | null;
  phone?: string | null;
  title?: string | null;
}

/** The contacts the document's contact may be chosen among (`customers:contacts-view`). */
export const customerContactsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "customer-contacts", customerId],
    retry: false,
    queryFn: async ({ signal }) => {
      const answer = await request<{ data: CustomerContactOption[] }>(`/api/v1/customers/${customerId}/contacts`, {
        signal,
      });
      return answer.data;
    },
  });
