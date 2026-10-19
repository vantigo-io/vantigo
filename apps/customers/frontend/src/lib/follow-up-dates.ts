import type { TimelineFollowUp } from "../api/timeline";

/**
 * "Today" on the UTC calendar. The server judges today on the workspace's
 * business day (time zones design D7), which this page cannot know until the
 * shell hands it the business time zone (the design's delivery F); until then
 * the UTC day stands in, so a browser west of Greenwich still does not read its
 * own local date here. The two disagree only between UTC midnight and the
 * business zone's — 22:00 to 24:00 UTC in an Oslo summer.
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
