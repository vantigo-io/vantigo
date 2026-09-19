/**
 * What the contract accepts, so a form says so before the API has to. The
 * backend trims first and measures the trimmed value; every check here does
 * the same.
 */
export const TASK_TITLE_MAX = 200;
export const TASK_DESCRIPTION_MAX = 4000;
export const CHECKLIST_TEXT_MAX = 500;
export const COMMENT_BODY_MAX = 4000;

/** How much of a free-text field an accessible name quotes before an item's own text runs on too long. */
const LABEL_EXCERPT_MAX = 60;

/** A row's own text, cut to a sensible length for naming its button — the text itself, not a summary of it. */
export const excerptForLabel = (text: string, max = LABEL_EXCERPT_MAX): string =>
  text.length > max ? `${text.slice(0, max)}…` : text;

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
