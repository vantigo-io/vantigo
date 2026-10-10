import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components, operations } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * A reminder letter (invoices payments and reminders design D10): made by a
 * run without its facts, which are written when it is sent or printed and
 * frozen once sent. This module owns every call about a letter; the invoice
 * page and the Overdue area both read and act through it. The wire has no
 * read of one letter by its id: an invoice carries its letters, and the list
 * filters by invoice or run.
 */
export type Reminder = Schemas["InvoicesReminder"];
export type ReminderStatus = Reminder["status"];
export type ReminderList = Schemas["PaginatedResponseOfInvoicesReminder"];
/** GET /reminders' filters: status, channel, invoiceId, runId, and page/pageSize. */
export type ReminderFilters = NonNullable<operations["getInvoicesReminders"]["parameters"]["query"]>;

/** A letter's statuses, in the order a letter passes through them. */
export const REMINDER_STATUSES: readonly ReminderStatus[] = [
  "queued",
  "awaiting_print",
  "printed",
  "sent",
  "failed",
  "withdrawn",
];

/** The statuses a person may withdraw a letter in — but not while it is being sent (D10). */
export const WITHDRAWABLE: ReadonlySet<ReminderStatus> = new Set(["queued", "awaiting_print", "printed", "failed"]);

/** The statuses a letter has a stored PDF in. */
export const WITH_PDF: ReadonlySet<ReminderStatus> = new Set(["printed", "sent"]);

const query = (filters: ReminderFilters): string => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined) params.set(key, String(value));
  }
  const text = params.toString();
  return text ? `?${text}` : "";
};

/** The letters, any status, filtered as GET /reminders takes it (`invoices:access`). */
export const remindersQueryOptions = (filters: ReminderFilters = {}) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "reminders", filters],
    queryFn: ({ signal }) => request<ReminderList>(`/api/v1/invoices/reminders${query(filters)}`, { signal }),
  });

/** Where a printed or sent letter's stored PDF is, for `PdfButton` (`invoices:access`). */
export const reminderPdfUrl = (id: number): string => appUrl(`/api/v1/invoices/reminders/${id}/pdf`);

/**
 * Withdraws a letter with the person's reason (`invoices:payments`): a queued,
 * awaiting-print, printed or failed one, never one being sent
 * (`reminder_not_withdrawable`). The answer is the letter; the caller
 * invalidates the invoice it belongs to.
 */
export const withdrawReminder = (id: number, reason: string): Promise<Reminder> =>
  request<Reminder>(`/api/v1/invoices/reminders/${id}/withdraw`, json("POST", { reason }));

/** Puts a failed letter back in the queue (`invoices:payments`); any other is `reminder_not_failed`. */
export const retryReminder = (id: number): Promise<Reminder> =>
  request<Reminder>(`/api/v1/invoices/reminders/${id}/retry`, { method: "POST" });
