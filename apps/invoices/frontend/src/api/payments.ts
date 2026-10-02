import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** One registration of money received against an issued invoice, a removed one included (D2). */
export type InvoicePayment = Schemas["InvoicesPayment"];
export type PaymentInput = Schemas["InvoicesPaymentRequest"];

/**
 * Registers a payment. The answer is the document without its send defaults
 * (reading 5b), so the caller invalidates the document rather than setting
 * this answer into the cache.
 */
export const registerPayment = (id: number, input: PaymentInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/payments`, json("POST", input));

/** Removes a registration with a reason; it stays on the document, struck through. Never undone. */
export const removePayment = (id: number, paymentId: number, reason: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/payments/${paymentId}/remove`, json("POST", { reason }));
