import type { components } from "../api-schema";
import { json, request } from "./request";

type Schemas = components["schemas"];

/** An issued invoice's eFaktura state (invoices phase 6 design I31). */
export type EfakturaState = Schemas["InvoicesEfakturaState"];
/** An AvtaleGiro claim (I33). */
export type AvtaleGiroClaim = Schemas["InvoicesAvtaleGiroClaim"];
/** The send's answer: the document, and whether AvtaleGiro went too. */
export type SendEfakturaResult = Schemas["InvoicesSendEfakturaResponse"];
/** The consumer provider's settings (I29). */
export type ConsumerProvider = Schemas["InvoicesConsumerProviderResponse"];
export type ConsumerProviderRequest = Schemas["InvoicesConsumerProviderRequest"];
/** A customer's eFaktura lookup (I30). */
export type ConsumerLookup = Schemas["InvoicesConsumerLookup"];
/** One AvtaleGiro mandate (I32). */
export type AvtaleGiroMandate = Schemas["InvoicesAvtaleGiroMandate"];
/** The verify's answer. */
export type VerifyResult = Schemas["InvoicesAccessPointVerifyResponse"];

/**
 * Queues an issued invoice as eFaktura, with its AvtaleGiro claim when one
 * can go. A 409 carries its code — `efaktura_not_found` with
 * `efakturaReason` — a 502 and a 503 theirs, on the error.
 */
export const sendEfaktura = (id: number): Promise<SendEfakturaResult> =>
  request<SendEfakturaResult>(`/api/v1/invoices/${id}/send-efaktura`, { method: "POST" });

const providerPath = "/api/v1/invoices/settings/consumer-provider";

export const getConsumerProvider = (): Promise<ConsumerProvider> => request<ConsumerProvider>(providerPath);

export const putConsumerProvider = (body: ConsumerProviderRequest): Promise<ConsumerProvider> =>
  request<ConsumerProvider>(providerPath, json("PUT", body));

export const deleteConsumerProvider = (): Promise<void> => request<void>(providerPath, { method: "DELETE" });

export const verifyConsumerProvider = (): Promise<VerifyResult> =>
  request<VerifyResult>(`${providerPath}/verify`, { method: "POST" });

/** Turns AvtaleGiro on or off; on is refused by the KID's room (reading 12). */
export const setAvtaleGiro = (enabled: boolean): Promise<{ enabled: boolean }> =>
  request<{ enabled: boolean }>("/api/v1/invoices/settings/avtalegiro", json("PUT", { enabled }));

const lookupPath = (customerId: number) => `/api/v1/invoices/customers/${customerId}/efaktura-lookup`;

export const getConsumerLookup = (customerId: number): Promise<ConsumerLookup> =>
  request<ConsumerLookup>(lookupPath(customerId));

/** Looks the customer up at the provider now. */
export const checkConsumerLookup = (customerId: number): Promise<ConsumerLookup> =>
  request<ConsumerLookup>(lookupPath(customerId), { method: "POST" });

/** The mandates; `unresolved` lists only those naming no customer. */
export const listMandates = (unresolved: boolean): Promise<{ items: AvtaleGiroMandate[] }> =>
  request<{ items: AvtaleGiroMandate[] }>(
    `/api/v1/invoices/avtalegiro/mandates${unresolved ? "?unresolved=true" : ""}`,
  );

/**
 * Assigns an unresolved mandate to a customer; one whose KID carries another
 * customer number needs `confirmMismatch` and a reason.
 */
export const resolveMandate = (
  mandateId: number,
  customerId: number,
  mismatch?: { confirmMismatch: boolean; reason: string },
): Promise<AvtaleGiroMandate> =>
  request<AvtaleGiroMandate>(
    `/api/v1/invoices/avtalegiro/mandates/${mandateId}/resolve`,
    json("POST", { customerId, ...mismatch }),
  );

/** Takes a mandate's customer away again. */
export const unassignMandate = (mandateId: number): Promise<AvtaleGiroMandate> =>
  request<AvtaleGiroMandate>(`/api/v1/invoices/avtalegiro/mandates/${mandateId}/unassign`, { method: "POST" });
