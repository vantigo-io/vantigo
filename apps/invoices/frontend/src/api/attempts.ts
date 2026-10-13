import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { downloadFile } from "./export";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** One Vipps payment attempt of an invoice (invoices payments and reminders design D16). */
export type PaymentAttempt = Schemas["InvoicesPaymentAttempt"];
export type PaymentAttemptState = PaymentAttempt["state"];
/** An on-site payment request as its create answers it: the attempt, and the QR code while Vipps waits. */
export type OnSitePayment = Schemas["InvoicesOnSitePayment"];
/** An on-site request's state as the tablet polls it, with the same QR code again while it is `created`. */
export type OnSitePaymentStatus = Schemas["InvoicesOnSitePaymentStatus"];
/** One Oslo day's Vipps payments with the totals per flow and the day's reconciliation (D17 rule 4). */
export type VippsPaymentsDay = Schemas["InvoicesVippsPaymentsDay"];
export type VippsPayment = Schemas["InvoicesVippsPayment"];
export type VippsDayReconciliation = Schemas["InvoicesVippsDayReconciliation"];

/**
 * The states in which Vipps or the payments worker is done with an attempt:
 * nothing more happens to it, so nothing is polled and nothing is abandoned.
 */
export const FINISHED_ATTEMPT_STATES: readonly PaymentAttemptState[] = [
  "captured",
  "cancelled",
  "aborted",
  "expired",
  "terminated",
  "capture_failed",
  "failed",
  "abandoned",
];

/**
 * Abandons a stuck attempt (`invoices:manage`) once the person has checked
 * the Vipps portal: the reason, 1–500 characters, and the acknowledgement that
 * nothing was captured. Refused 409 `attempt_not_abandonable` while Vipps
 * holds it or shows money captured.
 */
export const abandonAttempt = (
  attemptId: number,
  input: Schemas["InvoicesPaymentAttemptAbandonRequest"],
): Promise<PaymentAttempt> =>
  request<PaymentAttempt>(`/api/v1/invoices/payment-attempts/${attemptId}/abandon`, json("POST", input));

/** Asks Vipps for an on-site payment of the invoice's whole open amount; the answer carries the QR code. */
export const createPaymentRequest = (invoiceId: number): Promise<OnSitePayment> =>
  request<OnSitePayment>(`/api/v1/invoices/${invoiceId}/payment-requests`, json("POST", { flow: "on_site" }));

/** Reads an on-site request's state from the database — never Vipps — as the tablet polls it. */
export const readPaymentRequest = (invoiceId: number, attemptId: number, signal?: AbortSignal) =>
  request<OnSitePaymentStatus>(`/api/v1/invoices/${invoiceId}/payment-requests/${attemptId}`, { signal });

export const paymentRequestQueryOptions = (invoiceId: number, attemptId: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "payment-request", invoiceId, attemptId],
    queryFn: ({ signal }) => readPaymentRequest(invoiceId, attemptId, signal),
  });

/**
 * Cancels an on-site request the customer has not approved. Refused 409
 * `payment_request_active` while Vipps has not confirmed it yet, or once the
 * customer approved it.
 */
export const cancelPaymentRequest = (invoiceId: number, attemptId: number): Promise<OnSitePaymentStatus> =>
  request<OnSitePaymentStatus>(`/api/v1/invoices/${invoiceId}/payment-requests/${attemptId}/cancel`, {
    method: "POST",
  });

/** The day's Vipps payments (`invoices:access`), the day an Oslo date. */
export const vippsPaymentsDayQueryOptions = (date: string) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "vipps-payments", date],
    queryFn: ({ signal }) =>
      request<VippsPaymentsDay>(`/api/v1/invoices/vipps-payments?${new URLSearchParams({ date }).toString()}`, {
        signal,
      }),
  });

/** The same day as a CSV file, fetched so a refusal can be said rather than opened as a page. */
export const downloadVippsPaymentsCsv = (date: string) =>
  downloadFile(
    `/api/v1/invoices/vipps-payments.csv?${new URLSearchParams({ date }).toString()}`,
    `vipps-payments-${date}.csv`,
  );

/**
 * Records a day as reconciled (`invoices:payments`): once, after it is over,
 * with an optional note. Refused 409 `day_not_over` or `day_already_reconciled`.
 */
export const reconcileVippsDay = (input: Schemas["InvoicesVippsDayReconciliationRequest"]) =>
  request<VippsDayReconciliation>("/api/v1/invoices/vipps-payments/reconciliations", json("POST", input));
