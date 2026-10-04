import type { components } from "../api-schema";
import { downloadFile } from "./export";
import type { InvoiceDocument } from "./invoices";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** An issued document's EHF state (EHF and KID design D10). */
export type EhfState = Schemas["InvoicesEhfState"];
/** One EHF transmission of an issued document (D9). */
export type Transmission = Schemas["InvoicesTransmission"];
/** A person's verdict on an unconfirmed transmission. */
export type TransmissionResolution = Schemas["InvoicesTransmissionResolution"];

/**
 * Queues an issued document's EHF for the Peppol network (D8). A 409 carries
 * its code — `peppol_not_receivable` with `peppolRegistered` and
 * `peppolCanReceive`, `ehf_invalid` with `rules` — a 502 and a 503 theirs, on
 * the error. The answer is the document without its send defaults, so the
 * caller invalidates `[INVOICES_QUERY_KEY]` rather than setting it.
 */
export const sendEhf = (id: number): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/send-ehf`, { method: "POST" });

/** Cancels a queued transmission that was never attempted; anything else is 409 `transmission_not_cancellable`. */
export const cancelTransmission = (id: number, transmissionId: number): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/transmissions/${transmissionId}/cancel`, { method: "POST" });

/** Records a person's verdict on an unconfirmed transmission; anything else is 409 `transmission_not_resolvable`. */
export const resolveTransmission = (
  id: number,
  transmissionId: number,
  resolution: TransmissionResolution,
): Promise<InvoiceDocument> =>
  request<InvoiceDocument>(`/api/v1/invoices/${id}/transmissions/${transmissionId}/resolve`, json("POST", resolution));

/** Where a transmission's stored UBL is downloaded. */
export const ublUrl = (id: number, transmissionId: number): string =>
  `/api/v1/invoices/${id}/transmissions/${transmissionId}/ubl`;

/**
 * The stored UBL, fetched rather than navigated to, so a refusal — a store
 * that is down, an object gone — is said in words, never shown as JSON.
 */
export const downloadUbl = (id: number, transmissionId: number, fallback: string) =>
  downloadFile(ublUrl(id, transmissionId), fallback);
