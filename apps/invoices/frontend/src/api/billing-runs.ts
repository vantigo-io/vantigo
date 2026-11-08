import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { BILLING_RUN_POLL_MS } from "../lib/billing-runs";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * Billing runs (energy consumption billing design D10): one invoice per
 * customer with consumption due, drafted — and issued, when the run issues —
 * item by item by the invoices-billing worker.
 */
export type BillingRun = Schemas["InvoicesBillingRun"];
export type BillingRunCounts = Schemas["InvoicesBillingRunCounts"];
export type BillingRunList = Schemas["PaginatedResponseOfInvoicesBillingRun"];
export type BillingRunDetail = Schemas["InvoicesBillingRunDetail"];
export type BillingRunItem = Schemas["InvoicesBillingRunItem"];
export type BillingRunDocument = Schemas["InvoicesBillingRunDocument"];
export type BillingRunInput = Schemas["InvoicesBillingRunRequest"];

/** A run's statuses, in the order a run goes through them. */
export const BILLING_RUN_STATUSES = ["queued", "running", "done", "cancelled"] as const;

/** An item's statuses, in the order the counts and the filter show them. */
export const BILLING_ITEM_STATUSES = ["queued", "drafted", "issued", "skipped", "failed", "cancelled"] as const;

/** A run still at work: the worker carries its items, so its page polls and it may be cancelled. */
export const runIsLive = (run: Pick<BillingRun, "status">) => run.status === "queued" || run.status === "running";

/**
 * Listens to the billing runs' live topic (live design K40) and refetches on an event. The list
 * refetches on any; a run's page passes its id and ignores another run's event, while an event
 * with no id (the worker's sweep) and a resync refetch whatever is shown. Returns whether the
 * topic is live: the pages poll only while it is not.
 */
export function useBillingRunsLive(runId?: number): boolean {
  const client = useQueryClient();
  return useLiveTopic("invoices.billing-runs", (event) => {
    if (runId === undefined) {
      void client.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "billing-runs"] });
      return;
    }
    if ("ids" in event && event.ids.length > 0 && !event.ids.includes(String(runId))) return;
    void client.invalidateQueries({ queryKey: [INVOICES_QUERY_KEY, "billing-run", runId] });
  });
}

/**
 * The runs, newest first (`invoices:access`). Read again every few seconds while one is still
 * at work, unless `live` (useBillingRunsLive): live, the list is refetched on an event.
 */
export const billingRunsQueryOptions = (page: number, live = false) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "billing-runs", page],
    queryFn: ({ signal }) => request<BillingRunList>(`/api/v1/invoices/billing-runs?page=${page}`, { signal }),
    refetchInterval: (query) => (!live && query.state.data?.data.some(runIsLive) ? BILLING_RUN_POLL_MS : false),
  });

/**
 * One run and a page of its items, of one status when `status` names one (`invoices:access`).
 * Read again every few seconds while the run is at work, unless `live` (useBillingRunsLive).
 */
export const billingRunQueryOptions = (runId: number, status: string | null, page: number, live = false) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "billing-run", runId, status, page],
    queryFn: ({ signal }) =>
      request<BillingRunDetail>(
        `/api/v1/invoices/billing-runs/${runId}?${status ? `status=${status}&` : ""}page=${page}`,
        { signal },
      ),
    refetchInterval: (query) => {
      const run = query.state.data?.run;
      return !live && run && runIsLive(run) ? BILLING_RUN_POLL_MS : false;
    },
  });

/** POST /billing-runs: the run, queued — 201. The caller invalidates every invoices query. */
export const postBillingRun = (input: BillingRunInput): Promise<BillingRun> =>
  request<BillingRun>("/api/v1/invoices/billing-runs", json("POST", input));

/** POST /billing-runs/{id}/cancel: the run, cancelled. */
export const cancelBillingRun = (runId: number): Promise<BillingRun> =>
  request<BillingRun>(`/api/v1/invoices/billing-runs/${runId}/cancel`, { method: "POST" });
