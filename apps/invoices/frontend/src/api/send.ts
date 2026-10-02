import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { ApiValidationError, json, readJson, sessionExpired } from "./request";

type Schemas = components["schemas"];

/** What the Send dialog opens with: the recipient, the delivery preference and the send's warnings (D4). */
export type SendDefaults = Schemas["InvoicesSendDefaults"];
/** One logged send; `recipient` is absent for a reader without `invoices:issue`, `''` once anonymised. */
export type InvoiceDelivery = Schemas["InvoicesDelivery"];

/** A refusal's body: a problem's code (409, 502, 503) or the rate limiter's `{error: {code}}` (429). */
interface SendRefusal {
  title?: string | null;
  detail?: string | null;
  code?: string | null;
  errors?: Record<string, string[]>;
  error?: { code?: string; message?: string };
}

/**
 * Sends an issued document by e-mail, to `recipient` when one overrides the
 * customer's invoice e-mail. Fetched here rather than through the shared
 * client, which keeps a problem's `code` on a 409 only: a send is refused with
 * 502 `mail_failed` and 503 `mail_unavailable` too, and the limiter's 429
 * carries `{"error": {"code": "rate_limited"}}` — each thrown carrying its
 * code and body, as `fetchPdf` does, so the dialog words it in the reader's
 * language. The answer carries the send defaults, but the caller invalidates
 * the document anyway, as after every write (reading 5b).
 */
export const sendInvoice = async (id: number, recipient?: string): Promise<InvoiceDocument> => {
  const response = await fetch(appUrl(`/api/v1/invoices/${id}/send`), {
    ...json("POST", recipient ? { recipient } : {}),
    credentials: "include",
  });
  if (response.status === 401) await sessionExpired();
  if (response.ok) return readJson<InvoiceDocument>(response);
  const problem = await readJson<SendRefusal>(response).catch((): SendRefusal => ({}));
  if (response.status === 400 && problem.errors) {
    throw new ApiValidationError(problem.title ?? "Invalid recipient", problem.errors, 400);
  }
  const message = problem.detail ?? problem.title ?? problem.error?.message ?? response.statusText;
  throw Object.assign(new Error(message), {
    status: response.status,
    code: problem.code ?? problem.error?.code ?? undefined,
    problem,
  });
};
