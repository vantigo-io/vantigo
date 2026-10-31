/**
 * What the contract accepts on a delivery milestone, so a form says so before the API
 * has to. The backend trims first and measures the trimmed value.
 */
export const DELIVERY_MILESTONE_NAME_MAX = 200;
export const DELIVERY_MILESTONE_DESCRIPTION_MAX = 2000;

export const deliveryMilestoneStatuses = ["open", "completed"] as const;

export type DeliveryMilestoneStatus = (typeof deliveryMilestoneStatuses)[number];

type StatusPresentation = { labelKey: string; color: string };

const presentation: Record<DeliveryMilestoneStatus, StatusPresentation> = {
  open: { labelKey: "deliveryMilestoneStatusOpen", color: "blue" },
  completed: { labelKey: "deliveryMilestoneStatusCompleted", color: "teal" },
};

/** The `projects` catalog key naming this delivery milestone status. */
export const deliveryMilestoneStatusLabelKey = (status: DeliveryMilestoneStatus): string =>
  presentation[status].labelKey;

/** The Mantine colour a delivery milestone's status badge carries. */
export const deliveryMilestoneStatusColor = (status: DeliveryMilestoneStatus): string => presentation[status].color;

/** Whether a status string from the API is one this frontend knows. */
export const isDeliveryMilestoneStatus = (value: string): value is DeliveryMilestoneStatus =>
  (deliveryMilestoneStatuses as readonly string[]).includes(value);
