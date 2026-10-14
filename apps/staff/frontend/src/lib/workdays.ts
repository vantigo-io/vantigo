import { fromUtc, toUtc } from "./dates";

/** The longest range `GET /calendar` answers in one read. */
export const CALENDAR_MAX_DAYS = 93;

const addDays = (date: string, days: number): string => {
  const day = toUtc(date);
  day.setUTCDate(day.getUTCDate() + days);
  return fromUtc(day);
};

/**
 * The working days from `first` to `last`, both included: Monday to Friday,
 * without the public holidays `GET /calendar` answered for the range (plan
 * M13 — the holidays are the platform calendar's, never worked out here).
 * An estimate for the request sheet: the server's count is the one it keeps.
 */
export const workingDays = (first: string, last: string, holidays: readonly string[]): number => {
  const closed = new Set(holidays);
  let count = 0;
  for (let day = first; day <= last; day = addDays(day, 1)) {
    const weekday = toUtc(day).getUTCDay();
    if (weekday !== 0 && weekday !== 6 && !closed.has(day)) count += 1;
  }
  return count;
};

/** The reads of at most 93 days each that cover `first` to `last` without a gap. */
export const calendarWindows = (first: string, last: string): { from: string; to: string }[] => {
  const windows: { from: string; to: string }[] = [];
  for (let from = first; from <= last; ) {
    const end = addDays(from, CALENDAR_MAX_DAYS - 1);
    const to = end < last ? end : last;
    windows.push({ from, to });
    from = addDays(to, 1);
  }
  return windows;
};

/** The first and last day of `today`'s month. */
export const monthOf = (today: string): { from: string; to: string } => {
  const start = toUtc(`${today.slice(0, 7)}-01`);
  const end = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + 1, 0));
  return { from: fromUtc(start), to: fromUtc(end) };
};
