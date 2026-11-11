import type { ManufacturingInterval, ManufacturingWeekPattern } from "../api/work-centers";

/** The weekdays of a pattern, Monday first. */
export const weekdays = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"] as const;
export type Weekday = (typeof weekdays)[number];

/** At most two open intervals a day (M9). */
export const MAX_INTERVALS_PER_DAY = 2;
/** The grid every time is on (M9). */
export const GRID_MINUTES = 15;

/** The default pattern (M9): Monday to Friday 07:00–15:00, the weekend closed. */
export const defaultPattern = (): ManufacturingWeekPattern => ({
  monday: [{ start: "07:00", end: "15:00" }],
  tuesday: [{ start: "07:00", end: "15:00" }],
  wednesday: [{ start: "07:00", end: "15:00" }],
  thursday: [{ start: "07:00", end: "15:00" }],
  friday: [{ start: "07:00", end: "15:00" }],
  saturday: [],
  sunday: [],
});

/** The minutes since midnight of an `HH:MM` time; NaN when it is not one. "24:00" is 1440. */
export const minutesOf = (time: string): number => {
  const match = /^(\d{2}):(\d{2})$/.exec(time);
  if (!match) return Number.NaN;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  if (minutes > 59 || hours > 24 || (hours === 24 && minutes !== 0)) return Number.NaN;
  return hours * 60 + minutes;
};

const timeOf = (minutes: number) =>
  `${String(Math.floor(minutes / 60)).padStart(2, "0")}:${String(minutes % 60).padStart(2, "0")}`;

/** The times an interval can start at: 00:00 to 23:45 on the 15-minute grid. */
export const startTimes: readonly string[] = Array.from({ length: (24 * 60) / GRID_MINUTES }, (_, i) =>
  timeOf(i * GRID_MINUTES),
);
/** The times an interval can end at: 00:15 to 24:00 on the 15-minute grid. */
export const endTimes: readonly string[] = Array.from({ length: (24 * 60) / GRID_MINUTES }, (_, i) =>
  timeOf((i + 1) * GRID_MINUTES),
);

/** What is wrong with one interval or one day, as the server judges a pattern (M9). */
export type PatternProblem = "grid" | "order" | "overlap" | "tooMany";

export interface PatternIssue {
  day: Weekday;
  /** The interval at fault; absent when the day as a whole is (too many intervals). */
  index?: number;
  problem: PatternProblem;
}

const onGrid = (minutes: number) => Number.isFinite(minutes) && minutes % GRID_MINUTES === 0;

/**
 * Every problem with a pattern, mirroring the server's rule (M9): each time
 * on the 15-minute grid (`24:00` only as an end), each start before its end,
 * at most two intervals a day, and a day's two not overlapping. Empty when
 * the pattern can be saved.
 */
export const patternIssues = (pattern: ManufacturingWeekPattern): PatternIssue[] => {
  const issues: PatternIssue[] = [];
  for (const day of weekdays) {
    const intervals: readonly ManufacturingInterval[] = pattern[day] ?? [];
    if (intervals.length > MAX_INTERVALS_PER_DAY) issues.push({ day, problem: "tooMany" });
    intervals.forEach((interval, index) => {
      const start = minutesOf(interval.start);
      const end = minutesOf(interval.end);
      if (!onGrid(start) || !onGrid(end) || start >= 24 * 60) issues.push({ day, index, problem: "grid" });
      else if (start >= end) issues.push({ day, index, problem: "order" });
    });
    for (let i = 0; i < intervals.length; i++)
      for (let j = i + 1; j < intervals.length; j++) {
        const a = intervals[i];
        const b = intervals[j];
        if (minutesOf(a.start) < minutesOf(b.end) && minutesOf(b.start) < minutesOf(a.end))
          issues.push({ day, index: j, problem: "overlap" });
      }
  }
  return issues;
};

/** The day's open hours, in minutes: the length of its intervals. */
export const openMinutes = (intervals: readonly ManufacturingInterval[]): number =>
  intervals.reduce((sum, one) => sum + Math.max(0, minutesOf(one.end) - minutesOf(one.start)), 0);

/** A new interval for a day: after the day's last one when there is room, else the default shift. */
export const nextInterval = (intervals: readonly ManufacturingInterval[]): ManufacturingInterval => {
  const last = intervals.at(-1);
  if (!last) return { start: "07:00", end: "15:00" };
  const from = minutesOf(last.end);
  if (!Number.isFinite(from) || from >= 24 * 60) return { start: "07:00", end: "15:00" };
  return { start: timeOf(from), end: timeOf(Math.min(24 * 60, from + 4 * 60)) };
};
