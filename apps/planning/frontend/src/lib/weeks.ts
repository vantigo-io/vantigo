import { civilDateIn, getTimeZones } from "@vantigo/frontend-shell";

/**
 * Calendar days and weeks as Planning speaks them. A day is a plain
 * `YYYY-MM-DD` string, never a `Date`: the arithmetic below runs on UTC
 * midnight so a daylight-saving change never moves a day, and only `today()`
 * asks the clock — in the workspace's business time zone, the day the server
 * judges "today" by (design rule 1), not the browser's. A week is Monday to
 * Sunday and is named by its Monday, as the API names it.
 */

const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DAY_MS = 24 * 60 * 60 * 1000;

const pad = (value: number) => String(value).padStart(2, "0");

/** The UTC midnight of a civil date. */
export const toUtc = (date: string): Date => {
  const [, y, m, d] = ISO_DATE.exec(date) ?? [];
  return new Date(Date.UTC(Number(y), Number(m) - 1, Number(d)));
};

const fromUtc = (date: Date): string =>
  `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}`;

/** Whether a value holds a real calendar date (2026-02-30 does not). */
export const isIsoDate = (value: unknown): value is string =>
  typeof value === "string" && ISO_DATE.test(value) && fromUtc(toUtc(value)) === value;

/** Today on the business day: the calendar date in the workspace's time zone, which the shell holds. */
export const today = (): string => civilDateIn(getTimeZones().businessTimeZone);

export const addDays = (date: string, days: number): string => fromUtc(new Date(toUtc(date).getTime() + days * DAY_MS));

export const addWeeks = (date: string, weeks: number): string => addDays(date, weeks * 7);

/** The days from `from` to `to`, `to` not counted: 0 for the same day, negative when `to` is earlier. */
export const daysBetween = (from: string, to: string): number =>
  Math.round((toUtc(to).getTime() - toUtc(from).getTime()) / DAY_MS);

/** The Monday of the ISO week the date falls in; a Sunday belongs to the week before it. */
export const mondayOf = (date: string): string => {
  const weekday = toUtc(date).getUTCDay(); // 0 is Sunday
  return addDays(date, weekday === 0 ? -6 : 1 - weekday);
};

/** The Mondays of every week that has a day in `from`..`to`, in order; none when `to` is before `from`. */
export const weeksBetween = (from: string, to: string): string[] => {
  const mondays: string[] = [];
  if (to < from) return mondays;
  for (let monday = mondayOf(from); monday <= to; monday = addWeeks(monday, 1)) mondays.push(monday);
  return mondays;
};

/** The ISO 8601 week number of the week the date falls in (1 to 53). */
export const isoWeek = (date: string): number => {
  const thursday = addDays(mondayOf(date), 3);
  const firstThursday = addDays(mondayOf(`${thursday.slice(0, 4)}-01-04`), 3);
  return Math.round(daysBetween(firstThursday, thursday) / 7) + 1;
};

/**
 * A period as a person reads it, `from` to `to` inclusive, through the
 * page's date formatter: one date when they are the same day.
 */
export const formatRange = (
  from: string,
  to: string,
  formatDate: (value: Date, options?: Intl.DateTimeFormatOptions) => string,
): string => {
  const options: Intl.DateTimeFormatOptions = { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" };
  const start = formatDate(toUtc(from), options);
  return from === to ? start : `${start} – ${formatDate(toUtc(to), options)}`;
};
