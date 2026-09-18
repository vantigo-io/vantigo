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

/** The Mantine colour a status badge, or a grid cell holding an entry in it, carries. */
export const timeEntryStatusColor = (status: TimeEntryStatus): string => presentation[status].color;

/** Whether a status string from the API is one this frontend knows. */
export const isTimeEntryStatus = (value: string): value is TimeEntryStatus =>
  (timeEntryStatuses as readonly string[]).includes(value);

/** The statuses whose content the owner may still change: everything before submission, and a rejection. */
export const isEditableStatus = (status: TimeEntryStatus): boolean => status === "draft" || status === "rejected";
