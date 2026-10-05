/** A time entry's lifecycle (design D2), in the order an entry moves through it. */
export const timeEntryStatuses = ["draft", "submitted", "approved", "rejected", "invoiced"] as const;

export type TimeEntryStatus = (typeof timeEntryStatuses)[number];

type StatusPresentation = { labelKey: string; color: string };

const presentation: Record<TimeEntryStatus, StatusPresentation> = {
  draft: { labelKey: "statusDraft", color: "gray" },
  submitted: { labelKey: "statusSubmitted", color: "blue" },
  approved: { labelKey: "statusApproved", color: "green" },
  rejected: { labelKey: "statusRejected", color: "red" },
  invoiced: { labelKey: "statusInvoiced", color: "violet" },
};

/** The `time` catalog key naming this status. */
export const timeEntryStatusLabelKey = (status: TimeEntryStatus): string => presentation[status].labelKey;

/** The invoice the Invoices module stamped an entry with (invoices work design D1): its id and its number. */
export interface InvoicedBy {
  invoiceId: number;
  number: number;
}

/**
 * The words an entry's status badge says, as a catalog key and its values: an
 * entry the Invoices module invoiced names the invoice — "Invoiced by invoice
 * n" (invoices work design D18) — and every other status its own word.
 */
export const timeEntryStatusLabel = (
  status: TimeEntryStatus,
  invoicedBy?: InvoicedBy,
): { key: string; values?: Record<string, unknown> } =>
  status === "invoiced" && invoicedBy
    ? { key: "statusInvoicedBy", values: { number: invoicedBy.number } }
    : { key: timeEntryStatusLabelKey(status) };

/** The Mantine colour a status badge, or a grid cell holding an entry in it, carries. */
export const timeEntryStatusColor = (status: TimeEntryStatus): string => presentation[status].color;

/** Whether a status string from the API is one this frontend knows. */
export const isTimeEntryStatus = (value: string): value is TimeEntryStatus =>
  (timeEntryStatuses as readonly string[]).includes(value);

/** The statuses whose content the owner may still change: everything before submission, and a rejection. */
export const isEditableStatus = (status: TimeEntryStatus): boolean => status === "draft" || status === "rejected";
