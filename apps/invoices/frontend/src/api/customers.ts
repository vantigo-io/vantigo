import { queryOptions } from "@tanstack/react-query";
import { INVOICES_QUERY_KEY, json, request } from "./request";

/**
 * A customer as the buyer picker needs it: the customers module's own list,
 * read over HTTP because `contracts.CustomerDirectory` has no search (D1). It
 * needs `customers:view`, so the picker is only offered to a caller who holds
 * it; the invoices API itself takes a customer id and asks nothing more.
 *
 * The shape is typed here rather than generated: this package owns only the
 * invoices contract, and these four fields are all it reads of the answer.
 */
export interface CustomerOption {
  id: number;
  name: string;
  customerNumber: number;
  status: string;
}

interface CustomerListAnswer {
  data: CustomerOption[];
}

/**
 * A search of the customers. A buyer is picked among the active ones only —
 * the gates refuse the rest — while the list's filter offers every customer
 * (`anyStatus`), a disabled or an archived one too: their issued documents
 * are kept for the years the bookkeeping act (§ 13) asks, and are still found
 * by who they were for.
 */
export const customerSearchQueryOptions = (search: string, anyStatus = false) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "customers", search, anyStatus],
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ pageSize: "20" });
      if (anyStatus) params.set("includeArchived", "true");
      else params.set("status", "active");
      if (search.trim()) params.set("search", search.trim());
      const answer = await request<CustomerListAnswer>(`/api/v1/customers?${params.toString()}`, { signal });
      return answer.data;
    },
  });

interface BillingProfileLanguage {
  language?: string | null;
}

/**
 * The language a customer's documents are written in, from its billing
 * profile — the server's rule: English for "en", Norwegian otherwise. Read
 * over HTTP as the picker reads the list, so it needs `customers:view`.
 */
export const customerLanguageQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "customer-language", customerId],
    // A refusal — no customers:view after all — falls back at once to the
    // reader's language rather than retrying for seconds.
    retry: false,
    queryFn: async ({ signal }): Promise<"en" | "nb"> => {
      const profile = await request<BillingProfileLanguage>(`/api/v1/customers/${customerId}/billing-profile`, {
        signal,
      });
      return profile.language === "en" ? "en" : "nb";
    },
  });

/** One of a customer's addresses, as the customers module lists them: the fields the quick invoice shows. */
export interface CustomerAddress {
  id: number;
  type: string;
  isPrimary: boolean;
  line1: string;
  line2?: string | null;
  postalCode?: string | null;
  city?: string | null;
  country: string;
}

/**
 * The address a customer's invoice is written to, by the customers module's
 * own rule (invoice-ready customer design D3): the primary invoice address,
 * else the primary postal one, else none. Read over HTTP as the picker reads
 * the list, so it needs `customers:view`.
 */
export const customerInvoiceAddressQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "customer-address", customerId],
    retry: false,
    queryFn: async ({ signal }): Promise<CustomerAddress | null> => {
      const answer = await request<{ data: CustomerAddress[] }>(`/api/v1/customers/${customerId}/addresses`, {
        signal,
      });
      const primary = (type: string) => answer.data.find((a) => a.type === type && a.isPrimary);
      return primary("invoice") ?? primary("postal") ?? null;
    },
  });

/** A new customer as the quick invoice makes one: a name, a type and the address its invoice needs. */
export interface NewCustomerInput {
  name: string;
  type: "business" | "person";
  address: { line1: string; postalCode: string; city: string; country: string };
}

/**
 * Creates a customer through the customers module's API (`customers:create`)
 * and then its primary invoice address (`customers:update`), answering the new
 * customer's id. A customer whose address was refused still exists; the error
 * then says so, with its id.
 */
export const createCustomerWithAddress = async (input: NewCustomerInput): Promise<{ id: number }> => {
  const created = await request<{ id: number }>(
    "/api/v1/customers",
    json("POST", { name: input.name, type: input.type }),
  );
  try {
    await request(
      `/api/v1/customers/${created.id}/addresses`,
      json("POST", { type: "invoice", isPrimary: true, ...input.address }),
    );
  } catch (error) {
    throw Object.assign(error instanceof Error ? error : new Error(String(error)), { createdCustomerId: created.id });
  }
  return created;
};
