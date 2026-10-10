import type { components } from "../api-schema";
import { type CsvDownload, downloadFile } from "./export";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** An invoice's hold (invoices payments and reminders design D11): its latest, live or lifted. */
export type InvoiceHold = Schemas["InvoicesHold"];
/** An invoice's hand-off to a collection agency: its latest, live or withdrawn. */
export type InvoiceHandoff = Schemas["InvoicesHandoff"];
export type HandoffInput = Schemas["InvoicesHandoffRequest"];
export type HandoffWithdrawInput = Schemas["InvoicesHandoffWithdrawRequest"];
export type LiftInput = Schemas["InvoicesHoldLiftRequest"];
/**
 * What a hold, its lift, a hand-off and its withdrawal answer: the invoice,
 * and the letters no withdrawal reached — printed ones, which may be in the
 * post, and one being sent — for a person to pull and withdraw by hand.
 */
export type HoldOrHandoffResult = Schemas["InvoicesHoldOrHandoffResult"];
export type LetterLeft = Schemas["InvoicesLetterLeft"];

/** Puts an invoice the customer disputes on hold, with what is disputed (`invoices:payments`). */
export const placeHold = (id: number, note: string): Promise<HoldOrHandoffResult> =>
  request<HoldOrHandoffResult>(`/api/v1/invoices/${id}/hold`, json("POST", { note }));

/**
 * Lifts the hold (`invoices:payments`). `chargesAllowed` is always sent:
 * false — the objection had reasonable grounds — waives every fee and
 * compensation claimed and bars them for good; true waives nothing.
 */
export const liftHold = (id: number, input: LiftInput): Promise<HoldOrHandoffResult> =>
  request<HoldOrHandoffResult>(`/api/v1/invoices/${id}/hold/lift`, json("POST", input));

/** Records the hand-off to a collection agency, made outside Vantigo (`invoices:payments`). */
export const handOff = (id: number, input: HandoffInput): Promise<HoldOrHandoffResult> =>
  request<HoldOrHandoffResult>(`/api/v1/invoices/${id}/collection`, json("POST", input));

/** Records that the claim came back from the agency (`invoices:payments`). */
export const withdrawHandoff = (id: number, input: HandoffWithdrawInput): Promise<HoldOrHandoffResult> =>
  request<HoldOrHandoffResult>(`/api/v1/invoices/${id}/collection/withdraw`, json("POST", input));

/** The collection file of one invoice, for the agency (`invoices:access` and `invoices:payments`). */
export const downloadCollectionCsv = (invoiceId: number): Promise<CsvDownload> =>
  downloadFile(
    `/api/v1/invoices/collection-export.csv?${new URLSearchParams({ invoiceId: String(invoiceId) }).toString()}`,
    `invoices-collection-${invoiceId}.csv`,
  );
