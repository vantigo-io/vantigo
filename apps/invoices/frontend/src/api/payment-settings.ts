import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The payment settings (invoices payments and reminders design D14): the two
 * switches, the sales terms' and privacy notice's addresses, who acknowledged
 * the kontantsalg notice and when, and the revision a save names.
 */
export type PaymentSettings = Schemas["InvoicesPaymentSettings"];
/**
 * PUT /settings/payments' body, a full replace. Turning payment on site on
 * needs `acknowledgeKontantsalg: true` in the same body.
 */
export type PaymentSettingsInput = Schemas["InvoicesPaymentSettingsRequest"];

export const paymentSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "payment-settings"],
    queryFn: ({ signal }) => request<PaymentSettings>("/api/v1/invoices/settings/payments", { signal }),
  });

/**
 * Saves the payment settings at the revision read. Refused with 409
 * `public_url_missing` (pay links without `PUBLIC_BASE_URL`),
 * `payments_unavailable` (no Vipps keys) or a stale revision. The caller
 * invalidates `[INVOICES_QUERY_KEY]` — meta's availability follows it.
 */
export const putPaymentSettings = (input: PaymentSettingsInput): Promise<PaymentSettings> =>
  request<PaymentSettings>("/api/v1/invoices/settings/payments", json("PUT", input));
