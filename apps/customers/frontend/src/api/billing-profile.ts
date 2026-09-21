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

/**
 * The profile as it actually arrives. The server encodes an unset optional
 * field by leaving it out (`omitempty` on every nullable field of
 * `CustomerBillingProfile` in `apps/server/internal/customers/gen/api.gen.go`),
 * so a customer that has decided nothing answers exactly
 * `{"revision":3,"warnings":["no_invoice_address"]}` — and Go marshals a nil
 * `warnings` slice as null. Absent and null mean the same thing here (D4:
 * "not decided, the invoicing default applies"), so this shape is mapped to
 * the full one at the boundary and nothing downstream has to know.
 */
type RawCustomerBillingProfile = Partial<Omit<CustomerBillingProfile, "revision">> & { revision: number };

const normalizeBillingProfile = (raw: RawCustomerBillingProfile): CustomerBillingProfile => ({
  invoiceEmail: raw.invoiceEmail ?? null,
  reminderEmail: raw.reminderEmail ?? null,
  paymentTermsDays: raw.paymentTermsDays ?? null,
  currency: raw.currency ?? null,
  language: raw.language ?? null,
  invoiceDelivery: raw.invoiceDelivery ?? null,
  reminderDelivery: raw.reminderDelivery ?? null,
  peppolId: raw.peppolId ?? null,
  gln: raw.gln ?? null,
  buyerReference: raw.buyerReference ?? null,
  revision: raw.revision,
  warnings: raw.warnings ?? [],
});

async function fetchBillingProfile(customerId: number, signal?: AbortSignal): Promise<CustomerBillingProfile> {
  return normalizeBillingProfile(
    await request<RawCustomerBillingProfile>(`/api/v1/customers/${customerId}/billing-profile`, { signal }),
  );
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

/**
 * Replaces a customer's billing profile. Answers the fresh profile: a new
 * revision and freshly computed warnings — normalised like the GET's own
 * body, since the caller writes it straight into the card's query cache.
 */
export const updateBillingProfile = async (customerId: number, input: CustomerBillingProfileInput) =>
  normalizeBillingProfile(
    await request<RawCustomerBillingProfile>(`/api/v1/customers/${customerId}/billing-profile`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );
