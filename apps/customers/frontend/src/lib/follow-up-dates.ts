import type { TimelineFollowUp } from "../api/timeline";

/**
 * "Today" as the server counts it: the follow-up designs put every date-only
 * field on the UTC calendar (`civilDate(deps.Clock())`), so a browser west of
 * Greenwich must not read its own local date here — late afternoon in Oslo and
 * mid-morning in Seattle are the same follow-up day.
 */
export const utcToday = () => new Date().toISOString().slice(0, 10);

/**
 * Open and past its due date. Strictly past: a follow-up due TODAY is still
 * merely open, which is also how the API's `state=overdue` filter reads it.
 *
 * Shared, because two surfaces answer the same question about the same value —
 * the entry's follow-up line on the timeline card and the Follow-ups page's due
 * column — and two copies of a date comparison are two chances to disagree
 * about the boundary.
 */
export const isOverdue = (followUp: Pick<TimelineFollowUp, "dueOn" | "doneAt">) =>
  !followUp.doneAt && followUp.dueOn < utcToday();
