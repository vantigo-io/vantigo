import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** One registration of money received against an issued invoice, a removed one included (D2). */
export type InvoicePayment = Schemas["InvoicesPayment"];
export type PaymentInput = Schemas["InvoicesPaymentRequest"];

/**
 * Registers a payment. The answer is the document without its send defaults
 * (design D4), so the caller invalidates the document rather than setting
 * this answer into the cache.
 */
export const registerPayment = (id: number, input: PaymentInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/payments`, json("POST", input));

/** Removes a registration with a reason; it stays on the document, struck through. Never undone. */
export const removePayment = (id: number, paymentId: number, reason: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/payments/${paymentId}/remove`, json("POST", { reason }));

/** One write-off of an issued invoice, an undone one included (accounting design A16). */
export type InvoiceWriteOff = Schemas["InvoicesWriteOff"];
export type WriteOffInput = Schemas["InvoicesWriteOffRequest"];
export type WriteOffReason = Schemas["InvoicesWriteOffReason"];

/** Writes the invoice's whole open amount off. The caller invalidates the document. */
export const writeOff = (id: number, input: WriteOffInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/write-off`, json("POST", input));

/** Undoes the standing write-off with a reason; it stays on the document, struck through. */
export const undoWriteOff = (id: number, reason: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/write-off/undo`, json("POST", { reason }));
