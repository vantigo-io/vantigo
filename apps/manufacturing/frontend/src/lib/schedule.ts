import type { ManufacturingRescheduleInput, ManufacturingSchedule, ManufacturingScheduleBar } from "../api/schedule";

/**
 * The board's geometry (M36), kept apart from the page so it can be tested:
 * each day column is scaled over that day's open span — the earliest interval
 * start to the latest interval end of any work center that day — so a bar
 * sits at its planned time. A day no center is open on falls back to
 * 06:00–22:00 in the viewer's time.
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

/** A local date and time (`2026-11-12`, `07:30`) as epoch milliseconds in the viewer's zone. */
export const localInstant = (date: string, time: string): number => new Date(`${date}T${time}`).getTime();

/** The day columns' spans, one per day of the board. */
export const dayWindows = (schedule: Pick<ManufacturingSchedule, "from" | "days" | "workCenters">): DayWindow[] =>
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
      return { date, start: localInstant(date, FALLBACK_START), end: localInstant(date, FALLBACK_END) };
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

/** The reschedule request (M36): the start, and the work order's revision the board read. */
export const rescheduleBody = (
  bar: Pick<ManufacturingScheduleBar, "workOrderRevision">,
  start: number,
): ManufacturingRescheduleInput => ({ start: new Date(start).toISOString(), revision: bar.workOrderRevision });

/** The start a Move to… dialog's date and time name, snapped to the grid. */
export const movedStart = (date: string, time: string): number => snapToGrid(localInstant(date, time));

/** A bar's start as the Move to… dialog prefills it: its local date and time. */
export const localDateAndTime = (instant: string): { date: string; time: string } => {
  const at = new Date(instant);
  const pad = (value: number) => String(value).padStart(2, "0");
  return {
    date: `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}`,
    time: `${pad(at.getHours())}:${pad(at.getMinutes())}`,
  };
};
