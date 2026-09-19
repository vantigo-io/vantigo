/**
 * What the contract accepts on a milestone, so a form says so before the API
 * has to. The backend trims first and measures the trimmed value.
 */
export const MILESTONE_NAME_MAX = 200;
export const MILESTONE_DESCRIPTION_MAX = 2000;
export const MILESTONE_INVOICE_REFERENCE_MAX = 100;

/** The most a flat milestone amount may be, per the contract. */
export const MILESTONE_AMOUNT_MAX = 9999999999.99;

/** A milestone's status, in the order the invoice plan moves through it. */
export const milestoneStatuses = ["planned", "ready", "invoiced", "cancelled"] as const;

export type MilestoneStatus = (typeof milestoneStatuses)[number];

type StatusPresentation = { labelKey: string; color: string };

// Cancelled is as quiet as planned: it is the row nobody has to act on, and
// the plan already sets it apart by striking it through and listing it last.
const presentation: Record<MilestoneStatus, StatusPresentation> = {
  planned: { labelKey: "milestoneStatusPlanned", color: "gray" },
  ready: { labelKey: "milestoneStatusReady", color: "yellow" },
  invoiced: { labelKey: "milestoneStatusInvoiced", color: "green" },
  cancelled: { labelKey: "milestoneStatusCancelled", color: "gray" },
};

/** The `projects` catalog key naming this milestone status. */
export const milestoneStatusLabelKey = (status: MilestoneStatus): string => presentation[status].labelKey;

/** The Mantine colour a milestone's status badge carries. */
export const milestoneStatusColor = (status: MilestoneStatus): string => presentation[status].color;

/** Whether a status string from the API is one this frontend knows. */
export const isMilestoneStatus = (value: string): value is MilestoneStatus =>
  (milestoneStatuses as readonly string[]).includes(value);
