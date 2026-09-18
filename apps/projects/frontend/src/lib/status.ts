/** The project statuses the API accepts, in the order the list page's filter shows them. */
export const projectStatuses = ["planned", "active", "on-hold", "completed", "cancelled"] as const;

export type ProjectStatus = (typeof projectStatuses)[number];

type StatusPresentation = { labelKey: string; color: string };

const presentation: Record<ProjectStatus, StatusPresentation> = {
  planned: { labelKey: "statusPlanned", color: "gray" },
  active: { labelKey: "statusActive", color: "green" },
  "on-hold": { labelKey: "statusOnHold", color: "yellow" },
  completed: { labelKey: "statusCompleted", color: "blue" },
  cancelled: { labelKey: "statusCancelled", color: "red" },
};

/** The `projects` catalog key naming this status. */
export const projectStatusLabelKey = (status: ProjectStatus): string => presentation[status].labelKey;

/** The Mantine colour a status badge carries. */
export const projectStatusColor = (status: ProjectStatus): string => presentation[status].color;

/** Whether a status string from the API is one this frontend knows. */
export const isProjectStatus = (value: string): value is ProjectStatus =>
  (projectStatuses as readonly string[]).includes(value);
