import type { components } from "../api-schema";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** The quick invoice's body (invoices payments and reminders design D17): a customer, one to ten lines, a request id. */
export type QuickInvoiceInput = Schemas["InvoicesQuickInvoiceRequest"];
export type QuickInvoiceLine = Schemas["InvoicesQuickInvoiceLine"];
/** The issued invoice, and — when payment on site was asked for — the request made or why none was. */
export type QuickInvoiceAnswer = Schemas["InvoicesQuickInvoiceResponse"];

/**
 * Creates and issues a quick invoice in one step (`invoices:access`,
 * `invoices:create` and `invoices:issue`). The same `requestId` with the same
 * body again — a retry after a lost answer — answers the invoice the first
 * issued and does nothing else; another body with it is 409 `request_id_reused`.
 */
export const postQuickInvoice = (input: QuickInvoiceInput): Promise<QuickInvoiceAnswer> =>
  request<QuickInvoiceAnswer>("/api/v1/invoices/quick", json("POST", input));
