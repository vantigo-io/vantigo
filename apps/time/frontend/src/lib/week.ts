/**
 * Calendar days and hours as the time module speaks them. A day is a plain
 * `YYYY-MM-DD` string, never a `Date`: the arithmetic below runs on UTC
 * midnight so a daylight-saving change never moves a day, and only `today()`
 * asks the local clock, because "this week" is the caller's own week.
 */

const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;
const DAY_MS = 24 * 60 * 60 * 1000;

const pad = (value: number) => String(value).padStart(2, "0");

const toUtc = (date: string): Date => {
  const [, y, m, d] = ISO_DATE.exec(date) ?? [];
  return new Date(Date.UTC(Number(y), Number(m) - 1, Number(d)));
};

const fromUtc = (date: Date): string =>
  `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}`;

/** Whether a search param holds a real calendar date (2026-02-30 does not). */
export const isIsoDate = (value: unknown): value is string =>
  typeof value === "string" && ISO_DATE.test(value) && fromUtc(toUtc(value)) === value;

/** The caller's calendar date on their own clock. */
export const today = (): string => {
  const now = new Date();
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
};

export const addDays = (date: string, days: number): string => fromUtc(new Date(toUtc(date).getTime() + days * DAY_MS));

/** The Monday of the ISO week the date falls in; a Sunday belongs to the week before it. */
export const mondayOf = (date: string): string => {
  const weekday = toUtc(date).getUTCDay(); // 0 is Sunday
  return addDays(date, weekday === 0 ? -6 : 1 - weekday);
};

/** The week's seven days, Monday first. */
export const weekDays = (monday: string): string[] => Array.from({ length: 7 }, (_, i) => addDays(monday, i));

/** The largest duration one entry, and one day, may hold. */
export const MAX_HOURS = 24;

const round2 = (value: number) => Math.round(value * 100) / 100;

const DECIMAL = /^(\d*)(?:[.,](\d+))?$/;
const CLOCK = /^(\d{1,2}):(\d{2})$/;

/**
 * What a person typed into an hours field, in any of the three forms people
 * write a duration in — `7.5`, `7,5` and `7:30` — rounded to the two decimals
 * the API stores. Undefined for anything else, and for a duration that is not
 * more than zero or is longer than a day.
 */
export const parseHours = (input: string): number | undefined => {
  const text = input.trim();
  let hours: number | undefined;
  const clock = CLOCK.exec(text);
  if (clock) {
    const minutes = Number(clock[2]);
    if (minutes < 60) hours = Number(clock[1]) + minutes / 60;
  } else {
    const decimal = DECIMAL.exec(text);
    if (decimal && (decimal[1] !== "" || decimal[2] !== undefined)) {
      hours = Number(`${decimal[1] || "0"}.${decimal[2] ?? "0"}`);
    }
  }
  if (hours === undefined) return undefined;
  const rounded = round2(hours);
  return rounded > 0 && rounded <= MAX_HOURS ? rounded : undefined;
};

/** A duration for an input: at most two decimals, no trailing zeros, the given decimal separator. */
export const formatHours = (hours: number, separator: "." | "," = "."): string =>
  String(round2(hours)).replace(".", separator);

const minutesOf = (clock: string): number | undefined => {
  const match = CLOCK.exec(clock);
  return match ? Number(match[1]) * 60 + Number(match[2]) : undefined;
};

/**
 * The hours between a start and an end time on the same day (design D1), to
 * two decimals exactly as the server derives them, so the value the form
 * sends is the value the server recomputes. Undefined unless both times are
 * set and the end is after the start.
 */
export const hoursBetween = (start: string, end: string): number | undefined => {
  const from = minutesOf(start);
  const to = minutesOf(end);
  if (from === undefined || to === undefined || to <= from) return undefined;
  return Math.round(((to - from) * 100) / 60) / 100;
};

/** The My week page for the week starting on this Monday, as the host routes it. */
export const weekUrl = (monday: string): string => `/time?week=${monday}`;

/** The Day view for this date, as the host routes it. */
export const dayUrl = (date: string): string => `/time/day?date=${date}`;
