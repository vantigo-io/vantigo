import { civilDateIn, getTimeZones } from "@vantigo/frontend-shell";
import type { TimelineFollowUp } from "../api/timeline";

/**
 * "Today" on the workspace's business day (time zones design D6, D7): the day
 * the server judges an overdue follow-up, a future entry date and an
 * anonymisation date by, whoever is looking and wherever their browser is.
 * The shell holds the business time zone from the session; with no session it
 * is the browser's own.
 */
export const businessToday = () => civilDateIn(getTimeZones().businessTimeZone);

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
  !followUp.doneAt && followUp.dueOn < businessToday();
