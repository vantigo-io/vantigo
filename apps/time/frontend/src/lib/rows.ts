import type { TimeWeekRow } from "../api/weeks";

/** What a week-grid row logs time against: a project, optionally one of its lines, optionally a task. */
export type WeekRowRef = Omit<TimeWeekRow, "days">;

type Trackable = Pick<WeekRowRef, "projectId" | "billingLineId" | "taskId">;
type Named = Pick<WeekRowRef, "projectCode" | "billingLineCode" | "taskTitle">;

/** One row per trackable: the key the server groups a week's entries by. */
export const rowKey = (row: Trackable): string => `${row.projectId}:${row.billingLineId ?? ""}:${row.taskId ?? ""}`;

/** A week row's trackable alone, without the seven days the server filled it with. */
export const rowRef = (row: TimeWeekRow): WeekRowRef => ({
  projectId: row.projectId,
  projectCode: row.projectCode,
  projectName: row.projectName,
  billingLineId: row.billingLineId,
  billingLineCode: row.billingLineCode,
  trackableCode: row.trackableCode,
  taskId: row.taskId,
  taskTitle: row.taskTitle,
});

/** "KVEM1000 › PM › Write the spec", leaving out the parts the row does not have. */
export const rowLabel = (row: Named): string =>
  [row.projectCode, row.billingLineCode, row.taskTitle].filter(Boolean).join(" › ");
