import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** What the Send dialog opens with: the recipient, the delivery preference and the send's warnings (D4). */
export type SendDefaults = Schemas["InvoicesSendDefaults"];
/** One logged send; `recipient` is absent for a reader without `invoices:issue`, `''` once anonymised. */
export type InvoiceDelivery = Schemas["InvoicesDelivery"];

/**
 * Sends an issued document by e-mail, to `recipient` when one overrides the
 * customer's invoice e-mail. Through the shared client like every other
 * write: a 400 naming `recipient` is an `ApiValidationError`; a 409, 502 or
 * 503 problem and the limiter's 429 (`{"error": {"code": "rate_limited"}}`)
 * carry their code on the error, so the dialog words them in the reader's
 * language; a 401 signs the person out and a 404 is a `NotFoundError`. The
 * answer carries the send defaults and the new delivery row, but the caller
 * invalidates the document anyway, as after every write (design D4).
 */
export const sendInvoice = (id: number, recipient?: string): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/send`, json("POST", recipient ? { recipient } : {}));
