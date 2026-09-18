import { createFileRoute } from "@tanstack/react-router";
import { ProjectTasksTab } from "./-project-tasks-tab";

/** The Tasks tab's URL search params: one task, open in the drawer. */
interface ProjectTasksSearch {
  task?: number;
}

/**
 * A task id, or no task at all: a pasted or hand-edited link must never reach
 * the API as `tasks/NaN`, and a task nobody can resolve simply leaves the
 * drawer closed.
 */
const optionalTaskId = (value: unknown) => {
  if (typeof value !== "number" && typeof value !== "string") return undefined;
  const id = Number(value);
  return Number.isInteger(id) && id > 0 ? id : undefined;
};

export const Route = createFileRoute("/projects/$projectId/tasks")({
  // `?task=<id>` is the package's own deep link — its `taskUrl` builds it and
  // My tasks links to it — so the tab can be opened straight onto one task.
  validateSearch: (search: Record<string, unknown>): ProjectTasksSearch => ({ task: optionalTaskId(search.task) }),
  component: ProjectTasksTab,
});
