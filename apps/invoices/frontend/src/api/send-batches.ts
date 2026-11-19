import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { BILLING_RUN_POLL_MS } from "../lib/billing-runs";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * Send all (invoices phase 6 design I36): a batch of send jobs, one per
 * document chosen on the list or made by a billing run, carried by the
 * invoices-send worker — each by the customer's chosen channel, or by
 * eFaktura, EHF, e-mail or print as the server picks.
 */
export type SendBatch = Schemas["InvoicesSendBatch"];
export type SendBatchCounts = Schemas["InvoicesSendBatchCounts"];
export type SendBatchDetail = Schemas["InvoicesSendBatchDetail"];
export type SendJob = Schemas["InvoicesSendJob"];
export type SendBatchInput = Schemas["InvoicesSendBatchInput"];

/** A job's states, in the order the counts show them. */
export const SEND_JOB_STATES = [
  "queued",
  "sending",
  "sent",
  "handed_over",
  "awaiting_print",
  "printed",
  "skipped",
  "failed",
  "uncertain",
] as const;

/** The counts' key of each state. */
export const countKey: Record<(typeof SEND_JOB_STATES)[number], keyof SendBatchCounts> = {
  queued: "queued",
  sending: "sending",
  sent: "sent",
  handed_over: "handedOver",
  awaiting_print: "awaitingPrint",
  printed: "printed",
  skipped: "skipped",
  failed: "failed",
  uncertain: "uncertain",
};

/** A batch the worker is still carrying: its page polls while the live topic is not live. */
export const batchIsLive = (batch: Pick<SendBatch, "finishedAt">) => batch.finishedAt === undefined;

/**
 * Listens to the send batches' live topic (plan E4) and refetches on an event: a batch's page
 * passes its id and ignores another batch's event; a run's Sending card passes none and refetches
 * on any. Returns whether the topic is live: the pages poll only while it is not.
 */
export function useSendBatchesLive(batchId?: number): boolean {
  const client = useQueryClient();
  return useLiveTopic("invoices.send-batches", (event) => {
    if (batchId === undefined) {
      void client.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "send-batches"] });
      return;
    }
    if ("ids" in event && event.ids.length > 0 && !event.ids.includes(String(batchId))) return;
    void client.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "send-batch", batchId] });
  });
}

/** One batch and its jobs (`invoices:access`), read again every few seconds while it runs unless `live`. */
export const sendBatchQueryOptions = (batchId: number, live = false) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "send-batch", batchId],
    queryFn: ({ signal }) => request<SendBatchDetail>(`/api/v1/invoices/send-batches/${batchId}`, { signal }),
    refetchInterval: (query) => {
      const batch = query.state.data?.batch;
      return !live && batch && batchIsLive(batch) ? BILLING_RUN_POLL_MS : false;
    },
  });

/** A billing run's batches, newest first (`invoices:access`), read again while one runs unless `live`. */
export const runSendBatchesQueryOptions = (runId: number, live = false) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "send-batches", runId],
    queryFn: ({ signal }) =>
      request<{ data: SendBatch[] }>(`/api/v1/invoices/send-batches?billingRunId=${runId}`, { signal }),
    refetchInterval: (query) => (!live && query.state.data?.data.some(batchIsLive) ? BILLING_RUN_POLL_MS : false),
  });

/** POST /send-batches: the batch and its jobs, queued or skipped — 202. */
export const postSendBatch = (input: SendBatchInput): Promise<SendBatchDetail> =>
  request<SendBatchDetail>("/api/v1/invoices/send-batches", json("POST", input));

/** POST /send-batches/{id}/posted: the batch, its printed documents posted on `postedOn`. */
export const postSendBatchPosted = (batchId: number, postedOn: string): Promise<SendBatchDetail> =>
  request<SendBatchDetail>(`/api/v1/invoices/send-batches/${batchId}/posted`, json("POST", { postedOn }));

/** Where the batch's documents left for printing are, as one ZIP of their stored PDFs. */
export const sendBatchPrintUrl = (batchId: number) => `/api/v1/invoices/send-batches/${batchId}/print.zip`;
