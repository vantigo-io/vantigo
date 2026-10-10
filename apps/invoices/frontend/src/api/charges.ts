import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** An issued invoice's charges apart from its principal (invoices payments and reminders design D9). */
export type InvoiceCharges = Schemas["InvoicesCharges"];
/** One payment of an invoice's charges, a removed one included. */
export type ChargePayment = Schemas["InvoicesChargePayment"];
export type ChargePaymentInput = Schemas["InvoicesChargePaymentRequest"];
/** One charge released: a letter's fee or compensation whole, or an amount of interest. */
export type ChargeWaiver = Schemas["InvoicesChargeWaiver"];
export type WaiveInput = Schemas["InvoicesChargeWaiveRequest"];
export type WaiveReason = WaiveInput["reason"];
export type ChargeKind = ChargeWaiver["kind"];
/** A delivery recorded by hand (D8): handed over or posted. */
export type ManualDelivery = Schemas["InvoicesManualDelivery"];
export type ManualDeliveryInput = Schemas["InvoicesManualDeliveryRequest"];
export type ManualDeliveryKind = ManualDelivery["kind"];

/** The reasons a person may give a waiver; `deadline_met` is the bank match's own. */
export const WAIVE_REASONS: readonly WaiveReason[] = ["objection_upheld", "claimed_in_error", "goodwill"];

/** The two ways an invoice is handed over by hand, in the order the dialog offers them. */
export const MANUAL_DELIVERY_KINDS: readonly ManualDeliveryKind[] = ["handed_over", "posted"];

/**
 * Registers a payment of the invoice's charges (`invoices:payments`): the
 * payment's own fields, at most the charges outstanding. The answer is the
 * document without its send defaults, so the caller invalidates it.
 */
export const registerChargePayment = (id: number, input: ChargePaymentInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/charge-payments`, json("POST", input));

/** Removes a charge payment with a reason; it stays, struck through. */
export const removeChargePayment = (id: number, chargePaymentId: number, reason: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(
    `/api/v1/invoices/${id}/charge-payments/${chargePaymentId}/remove`,
    json("POST", { reason }),
  );

/** Waives charges (`invoices:payments`): each a letter and a kind, with a reason and a note. */
export const waiveCharges = (id: number, input: WaiveInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/charges/waive`, json("POST", input));

/** Records a delivery by hand (`invoices:issue`). */
export const recordDelivery = (id: number, input: ManualDeliveryInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/manual-deliveries`, json("POST", input));

/** Removes a delivery recorded by hand with a reason (`invoices:issue`); refused while a charge rests on it alone. */
export const removeDelivery = (id: number, deliveryId: number, reason: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/manual-deliveries/${deliveryId}/remove`, json("POST", { reason }));
