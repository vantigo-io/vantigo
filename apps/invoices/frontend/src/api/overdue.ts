import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components, operations } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The Overdue area's calls (invoices payments and reminders design D10, D12):
 * the overdue list, a run previewed and made, the runs, and paper letters
 * printed and posted in batches. A letter's own reads and writes — the
 * letters awaiting print among them — are `./reminders`.
 */
export type OverdueList = Schemas["InvoicesOverdueResponse"];
export type OverdueItem = Schemas["InvoicesOverdueItem"];
export type OverdueWarning = OverdueList["warnings"][number];
/** GET /overdue's filters: customerId, dueBefore, action, charges=outstanding, and page/pageSize. */
export type OverdueFilters = NonNullable<operations["getInvoicesOverdue"]["parameters"]["query"]>;
export type OverdueAction = NonNullable<OverdueFilters["action"]>;
/** The bank data's freshness: the latest booking day, whether it is stale, and the accounts imported as OCR giro. */
export type BankFreshness = Schemas["InvoicesBankFreshness"];
export type NextAction = Schemas["InvoicesNextAction"];
export type LetterFacts = Schemas["InvoicesLetterFacts"];
export type RunPreview = Schemas["InvoicesReminderRunPreview"];
export type RunPreviewLetter = Schemas["InvoicesRunPreviewLetter"];
export type RunPreviewHeld = Schemas["InvoicesRunPreviewHeld"];
export type RunItem = Schemas["InvoicesReminderRunItem"];
export type RunResult = Schemas["InvoicesReminderRunResult"];
export type RunSkip = Schemas["InvoicesReminderRunSkip"];
export type ReminderRun = Schemas["InvoicesReminderRun"];
export type ReminderRunList = Schemas["PaginatedResponseOfInvoicesReminderRun"];
export type ReminderRunDetail = Schemas["InvoicesReminderRunDetail"];
export type PrintBatch = Schemas["InvoicesPrintBatch"];
export type PrintBatchList = Schemas["PaginatedResponseOfInvoicesPrintBatch"];
export type PrintBatchResult = Schemas["InvoicesPrintBatchResult"];
export type PrintBatchLeftOut = Schemas["InvoicesPrintBatchLeftOut"];
export type PrintBatchPosted = Schemas["InvoicesPrintBatchPosted"];
export type PrintBatchWaiver = Schemas["InvoicesPrintBatchWaiver"];

/** The actions the list filters by, in the order a letter follows another. */
export const OVERDUE_ACTIONS: readonly OverdueAction[] = [
  "reminder",
  "collection_notice",
  "hand_off",
  "blocked",
  "waiting",
];

/** At most this many letters in one run (D10). */
export const MAX_RUN_ITEMS = 500;

/** At most this many letters in one print batch (D10). */
export const MAX_PRINT_LETTERS = 200;

/** A batch is printed for today or one of this many days on (D10). */
export const MAX_POST_ON_DAYS = 7;

const query = (filters: Record<string, string | number | boolean | undefined>): string => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const text = params.toString();
  return text ? `?${text}` : "";
};

/** The overdue list (`invoices:access`), judged whole before its action filter and page (25 a page by default). */
export const overdueQueryOptions = (filters: OverdueFilters) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "overdue", filters],
    queryFn: ({ signal }) => request<OverdueList>(`/api/v1/invoices/overdue${query(filters)}`, { signal }),
  });

/** The preview's narrowing: one customer, or the invoices due before a day — as the list narrows. */
export interface PreviewScope {
  customerId?: number;
  dueBefore?: string;
}

/**
 * The run's preview (`invoices:payments`): the letters as they would go
 * today, the invoices blocked or waiting, the bank data's freshness and the
 * warnings. Nothing is written. A POST, so it is read as a query keyed by its
 * scope and read again when the modal opens.
 */
export const runPreviewQueryOptions = (scope: PreviewScope) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "run-preview", scope],
    queryFn: ({ signal }) =>
      request<RunPreview>("/api/v1/invoices/reminder-runs", { ...json("POST", { dryRun: true, ...scope }), signal }),
    staleTime: 0,
    gcTime: 0,
  });

/**
 * Makes the run (`invoices:payments`): 1–500 invoices, each with the action
 * its preview showed; `acknowledgeStaleImport` confirms a run with charges on
 * stale bank data. The answer names the letters made and the invoices skipped.
 */
export const makeRun = (items: RunItem[], acknowledgeStaleImport: boolean): Promise<RunResult> =>
  request<RunResult>(
    "/api/v1/invoices/reminder-runs",
    json("POST", { dryRun: false, items, ...(acknowledgeStaleImport ? { acknowledgeStaleImport: true } : {}) }),
  );

/** The runs, newest first (`invoices:payments`). */
export const reminderRunsQueryOptions = (page: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "reminder-runs", page],
    queryFn: ({ signal }) => request<ReminderRunList>(`/api/v1/invoices/reminder-runs${query({ page })}`, { signal }),
  });

/** One run and its letters with their current status (`invoices:payments`). */
export const reminderRunQueryOptions = (runId: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "reminder-run", runId],
    queryFn: ({ signal }) => request<ReminderRunDetail>(`/api/v1/invoices/reminder-runs/${runId}`, { signal }),
  });

/** The print batches (`invoices:payments`), newest first; `posted` false for those not confirmed posted. */
export const printBatchesQueryOptions = (filters: { posted?: boolean; page: number }) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "print-batches", filters],
    queryFn: ({ signal }) =>
      request<PrintBatchList>(`/api/v1/invoices/reminder-print-batches${query(filters)}`, { signal }),
  });

/**
 * Prints letters awaiting print for the day they will be posted (`invoices:payments`):
 * each judged for that day; the letters that cannot go are left out with why.
 */
export const createPrintBatch = (reminderIds: number[], postOn: string): Promise<PrintBatchResult> =>
  request<PrintBatchResult>("/api/v1/invoices/reminder-print-batches", json("POST", { reminderIds, postOn }));

/** Where a batch's combined PDF is, for `PdfButton`. */
export const printBatchPdfUrl = (batchId: number): string =>
  appUrl(`/api/v1/invoices/reminder-print-batches/${batchId}/pdf`);

/**
 * Confirms a batch posted on `postedOn` — today or earlier, and its posting
 * day, else `reminder_posted_early` or `reminder_posted_late`: its printed
 * letters are sent, and the charges the day no longer supports are waived.
 */
export const confirmPosted = (batchId: number, postedOn: string): Promise<PrintBatchPosted> =>
  request<PrintBatchPosted>(`/api/v1/invoices/reminder-print-batches/${batchId}/posted`, json("POST", { postedOn }));

/** Returns a batch's printed letters to awaiting print, to be printed again for the day they go. */
export const reprint = (batchId: number): Promise<PrintBatch> =>
  request<PrintBatch>(`/api/v1/invoices/reminder-print-batches/${batchId}/reprint`, { method: "POST" });

/** A batch's state: open until it is confirmed posted or reprinted. */
export const printBatchState = (batch: PrintBatch): "open" | "posted" | "reprinted" =>
  batch.postedOn ? "posted" : batch.reprintedAt ? "reprinted" : "open";
