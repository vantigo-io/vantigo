import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export { ApiConflictError, ApiValidationError, NotFoundError } from "./request";

/** The languages a billing document can be produced in (invoice-ready customer design D4). */
export type BillingLanguage = "nb" | "en";

/** Every invoice delivery method the profile can name (design D4). */
export type InvoiceDeliveryMethod = "email" | "ehf" | "efaktura" | "paper";

/** Reminders cannot travel as EHF or eFaktura (design D4) — a narrower set than invoiceDelivery's own. */
export type ReminderDeliveryMethod = "email" | "paper";

/**
 * A customer's billing profile (design D1, D4): every field nullable,
 * meaning "not decided here — whoever invoices uses its own default". Unlike
 * `CustomerResponse` this is always fetched through its own sub-resource
 * (design D1's controller ruling keeps it out of `SafeCustomerResponse`).
 * `revision` is the same row-level token `CustomerResponse.revision` carries
 * (the billing profile lives on the customer row), and `warnings` is
 * recomputed at read time, never stored (design D4).
 */
export interface CustomerBillingProfile {
  invoiceEmail: string | null;
  reminderEmail: string | null;
  paymentTermsDays: number | null;
  currency: string | null;
  language: string | null;
  invoiceDelivery: string | null;
  reminderDelivery: string | null;
  peppolId: string | null;
  gln: string | null;
  buyerReference: string | null;
  revision: number;
  /** Machine-readable codes the UI explains — an unknown code is ignored (design D4). */
  warnings: string[];
}

async function fetchBillingProfile(customerId: number, signal?: AbortSignal): Promise<CustomerBillingProfile> {
  return request<CustomerBillingProfile>(`/api/v1/customers/${customerId}/billing-profile`, { signal });
}

export const customerBillingProfileQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "billing-profile"],
    queryFn: ({ signal }) => fetchBillingProfile(customerId, signal),
  });

/**
 * PUT .../billing-profile's own request body (design D1, D4): a full replace
 * of the customer's billing profile — every field present or null, absent
 * and null both meaning the field is cleared. `revision` is optional, as
 * `updateContactInfo`'s own is.
 */
export interface CustomerBillingProfileInput {
  invoiceEmail: string | null;
  reminderEmail: string | null;
  paymentTermsDays: number | null;
  currency: string | null;
  language: string | null;
  invoiceDelivery: string | null;
  reminderDelivery: string | null;
  peppolId: string | null;
  gln: string | null;
  buyerReference: string | null;
  revision?: number;
}

/** Replaces a customer's billing profile. Answers the fresh profile: a new revision and freshly computed warnings. */
export const updateBillingProfile = (customerId: number, input: CustomerBillingProfileInput) =>
  request<CustomerBillingProfile>(`/api/v1/customers/${customerId}/billing-profile`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
