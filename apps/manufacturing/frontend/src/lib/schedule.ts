import { civilDateIn, getTimeZones, instantFromWallClock, wallClockIn } from "@vantigo/frontend-shell";
import type { ManufacturingRescheduleInput, ManufacturingSchedule, ManufacturingScheduleBar } from "../api/schedule";

/**
 * The board's geometry (M36), kept apart from the page so it can be tested:
 * each day column is scaled over that day's open span — the earliest interval
 * start to the latest interval end of any work center that day — so a bar
 * sits at its planned time. A day no center is open on falls back to
 * 06:00–22:00 in the business time zone, the zone the board's days are
 * counted in. A time a person types — Move to… — means a moment in their own
 * zone (time zones design D6), and is shown back in it.
 */

/** One day column's span, as epoch milliseconds. */
export interface DayWindow {
  date: string;
  start: number;
  end: number;
}

/** The grid a drag and a move snap to (M9's calendars are on it). */
export const SNAP_MINUTES = 15;
const SNAP_MS = SNAP_MINUTES * 60_000;

const FALLBACK_START = "06:00";
const FALLBACK_END = "22:00";

/** A date and a wall-clock time (`2026-11-12`, `07:30`) in a zone, as epoch milliseconds. */
export const wallClockInstant = (date: string, time: string, zone: string): number =>
  instantFromWallClock(date, time, zone).getTime();

/** The day columns' spans, one per day of the board; a day nothing is open on spans the fallback in `zone`. */
export const dayWindows = (
  schedule: Pick<ManufacturingSchedule, "from" | "days" | "workCenters">,
  zone: string = getTimeZones().businessTimeZone,
): DayWindow[] =>
  Array.from({ length: schedule.days }, (_, index) => {
    const date = addDays(schedule.from, index);
    let start = Number.POSITIVE_INFINITY;
    let end = Number.NEGATIVE_INFINITY;
    for (const row of schedule.workCenters) {
      const day = row.days.find((one) => one.date === date);
      for (const interval of day?.intervals ?? []) {
        start = Math.min(start, new Date(interval.start).getTime());
        end = Math.max(end, new Date(interval.end).getTime());
      }
    }
    if (!Number.isFinite(start) || end <= start)
      return {
        date,
        start: wallClockInstant(date, FALLBACK_START, zone),
        end: wallClockInstant(date, FALLBACK_END, zone),
      };
    return { date, start, end };
  });

/** A calendar date `days` later (or earlier), on the date alone, so no zone moves it. */
export const addDays = (date: string, days: number): string => {
  const day = new Date(`${date}T00:00:00Z`);
  day.setUTCDate(day.getUTCDate() + days);
  return day.toISOString().slice(0, 10);
};

/** The column an instant falls in: the one whose span holds it, else the nearest. */
export const dayIndexOf = (instant: number, windows: readonly DayWindow[]): number => {
  let best = 0;
  let distance = Number.POSITIVE_INFINITY;
  windows.forEach((window, index) => {
    const away = instant < window.start ? window.start - instant : instant >= window.end ? instant - window.end : 0;
    if (away < distance) {
      best = index;
      distance = away;
    }
  });
  return best;
};

const clamp = (value: number, low: number, high: number) => Math.min(high, Math.max(low, value));

/** Where an instant sits across the row, 0 at the first column's left edge to 1 at the last's right. */
export const fractionOf = (instant: number, windows: readonly DayWindow[], index = dayIndexOf(instant, windows)) => {
  const window = windows[index];
  const within = clamp((instant - window.start) / (window.end - window.start), 0, 1);
  return (index + within) / windows.length;
};

/** The instant at a place across the row (0–1), inside the column it lands in. */
export const instantAt = (fraction: number, windows: readonly DayWindow[]): number => {
  const position = clamp(fraction, 0, 1) * windows.length;
  const index = clamp(Math.floor(position), 0, windows.length - 1);
  const window = windows[index];
  return window.start + clamp(position - index, 0, 1) * (window.end - window.start);
};

/** An instant rounded to the nearest quarter hour. */
export const snapToGrid = (instant: number): number => Math.round(instant / SNAP_MS) * SNAP_MS;

/** A bar's place in its row, as fractions of the row: drawn in the column its start falls in. */
export const barLayout = (
  bar: Pick<ManufacturingScheduleBar, "startsAt" | "endsAt">,
  windows: readonly DayWindow[],
) => {
  const start = new Date(bar.startsAt).getTime();
  const index = dayIndexOf(start, windows);
  const left = fractionOf(start, windows, index);
  const right = fractionOf(Math.min(new Date(bar.endsAt).getTime(), windows[index].end), windows, index);
  return { index, left, width: Math.max(right - left, 0) };
};

/**
 * Where a dragged bar would start: its left edge moved by the pointer's
 * travel across the row (`delta`, a fraction of the row), snapped to the grid.
 */
export const draggedStart = (
  bar: Pick<ManufacturingScheduleBar, "startsAt" | "endsAt">,
  delta: number,
  windows: readonly DayWindow[],
): number => snapToGrid(instantAt(barLayout(bar, windows).left + delta, windows));

/**
 * Where the work order starts once one of its bars is dropped at `barStart`
 * (M36): a bar is one piece of the work order, so the whole work order moves
 * by as far as the piece did — from its planned start, snapped to the grid.
 */
export const workOrderStart = (
  bar: Pick<ManufacturingScheduleBar, "startsAt" | "plannedStart">,
  barStart: number,
): number => {
  const from = new Date(bar.plannedStart ?? bar.startsAt).getTime();
  return snapToGrid(from + (barStart - new Date(bar.startsAt).getTime()));
};

/** The reschedule request (M36): the start, and the work order's revision the board read. */
export const rescheduleBody = (
  bar: Pick<ManufacturingScheduleBar, "workOrderRevision">,
  start: number,
  workCenterId?: number,
): ManufacturingRescheduleInput => ({
  start: new Date(start).toISOString(),
  revision: bar.workOrderRevision,
  // A group work order dropped on another member's row (manufacturing phase 2 M119).
  ...(workCenterId === undefined ? {} : { workCenterId }),
});

/**
 * The member row a bar was dropped on (manufacturing phase 2 M119): the work
 * center of the row under the pointer when it is another member of the bar's
 * group, else undefined — the bar moves along its own row.
 */
export const droppedMember = (
  bar: Pick<ManufacturingScheduleBar, "memberIds">,
  ownCenterId: number,
  rowCenterId: number | undefined,
): number | undefined =>
  rowCenterId !== undefined && rowCenterId !== ownCenterId && (bar.memberIds ?? []).includes(rowCenterId)
    ? rowCenterId
    : undefined;

/** The start a Move to… dialog's date and time name in the viewer's zone, snapped to the grid. */
export const movedStart = (date: string, time: string, zone: string = getTimeZones().timeZone): number =>
  snapToGrid(wallClockInstant(date, time, zone));

/** An instant as the Move to… dialog shows it: its date and time in the viewer's zone. */
export const dateAndTimeIn = (
  instant: string,
  zone: string = getTimeZones().timeZone,
): { date: string; time: string } => {
  const at = new Date(instant);
  return { date: civilDateIn(zone, at), time: wallClockIn(zone, at) };
};

/** What Move to… prefills for a bar: the start of its work order, not of the piece (M36). */
export const moveToPrefill = (
  bar: Pick<ManufacturingScheduleBar, "startsAt" | "plannedStart">,
  zone: string = getTimeZones().timeZone,
): { date: string; time: string } => dateAndTimeIn(bar.plannedStart ?? bar.startsAt, zone);
