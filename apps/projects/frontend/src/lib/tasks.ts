/** A task's status, in the order work moves through it — the order every board column and grouped list uses. */
export const taskStatuses = ["todo", "in-progress", "done"] as const;

export type TaskStatus = (typeof taskStatuses)[number];

type StatusPresentation = { labelKey: string; color: string };

const presentation: Record<TaskStatus, StatusPresentation> = {
  todo: { labelKey: "taskStatusTodo", color: "gray" },
  "in-progress": { labelKey: "taskStatusInProgress", color: "blue" },
  done: { labelKey: "taskStatusDone", color: "green" },
};

/** The `projects` catalog key naming this status. */
export const taskStatusLabelKey = (status: TaskStatus): string => presentation[status].labelKey;

/** The Mantine colour a task's status badge and board column carry. */
export const taskStatusColor = (status: TaskStatus): string => presentation[status].color;

/** Whether a status string from the API is one this frontend knows. */
export const isTaskStatus = (value: string): value is TaskStatus => (taskStatuses as readonly string[]).includes(value);

/**
 * The URL that opens one task in its project's Tasks tab. The host route owns
 * `/projects/$projectId/tasks` and reads `task` out of its search params to
 * open the drawer on it; this package only ever builds the link.
 */
export const taskUrl = (projectId: number, taskId: number): string => `/projects/${projectId}/tasks?task=${taskId}`;
