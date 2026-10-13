import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { request } from "./request";

/**
 * An issued invoice's live pay link (invoices payments and reminders design
 * D15). `url` and `qrSvg` come only when the server answers them — to a caller
 * holding `invoices:issue` while payment on site is available — and the app
 * never makes either itself.
 */
export type PayLink = components["schemas"]["InvoicesPayLink"];

/** Makes a pay link for an invoice that has none; the answer is the invoice with it. */
export const makePayLink = (invoiceId: number): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${invoiceId}/pay-link`, { method: "POST" });

/** Revokes the invoice's live pay link: it no longer pays the invoice. */
export const revokePayLink = (invoiceId: number): Promise<void> =>
  request<void>(`/api/v1/invoices/${invoiceId}/pay-link/revoke`, { method: "POST" });
