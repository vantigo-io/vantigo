import type { TaskDependency } from "../api/dependencies";
import type { Task } from "../api/tasks";

const MS_PER_DAY = 1000 * 60 * 60 * 24;

/** The timeline's scales, each a width for one calendar day. */
export const timelineZooms = ["day", "week", "month"] as const;

export type TimelineZoom = (typeof timelineZooms)[number];

/** Pixels per day: a day wide enough for its number, a week for its label, a month for a quarter on screen. */
export const ZOOM_DAY_WIDTH: Record<TimelineZoom, number> = { day: 40, week: 16, month: 5 };

export const isTimelineZoom = (value: unknown): value is TimelineZoom =>
  (timelineZooms as readonly unknown[]).includes(value);

/** Parse YYYY-MM-DD into UTC epoch days. */
export const dateToUtcDays = (dateStr: string): number => {
  const [y, m, d] = dateStr.split("-").map(Number);
  return Math.floor(Date.UTC(y, m - 1, d) / MS_PER_DAY);
};

/** Convert UTC epoch days back to YYYY-MM-DD. */
export const utcDaysToDateStr = (days: number): string => new Date(days * MS_PER_DAY).toISOString().split("T")[0];

/** A horizontal pointer distance as whole days at the current scale; a drag snaps to the nearest day. */
export const pixelsToDays = (dx: number, dayWidth: number): number => Math.round(dx / dayWidth) || 0;

/** What a drag holds on to: the bar's body moves both dates, an edge only its own. */
export type DragMode = "move" | "start" | "end";

export interface TaskDates {
  startDate?: string | null;
  dueDate?: string | null;
}

/**
 * A task's dates after being moved `days` whole days by `mode`. A task with
 * one date moves that date whatever was held, and an edge stops at the other
 * one: the due date never falls before the start.
 */
export const moveTaskDates = (dates: TaskDates, mode: DragMode, days: number): TaskDates => {
  const start = dates.startDate ? dateToUtcDays(dates.startDate) : null;
  const due = dates.dueDate ? dateToUtcDays(dates.dueDate) : null;
  const out = (s: number | null, d: number | null): TaskDates => ({
    startDate: s === null ? (dates.startDate ?? null) : utcDaysToDateStr(s),
    dueDate: d === null ? (dates.dueDate ?? null) : utcDaysToDateStr(d),
  });
  if (start === null || due === null || mode === "move") {
    return out(start === null ? null : start + days, due === null ? null : due + days);
  }
  if (mode === "start") return out(Math.min(start + days, due), due);
  return out(start, Math.max(due + days, start));
};

/** How many days the due date moved, 0 when either side has none. */
export const dueDelta = (before: TaskDates, after: TaskDates): number =>
  before.dueDate && after.dueDate ? dateToUtcDays(after.dueDate) - dateToUtcDays(before.dueDate) : 0;

/**
 * Every task reachable from `taskId` through finish-to-start links, direct and
 * indirect, each once: what the downstream shift would reach.
 */
export const downstreamTaskIds = (taskId: number, dependencies: TaskDependency[]): Set<number> => {
  const reached = new Set<number>();
  const queue = [taskId];
  while (queue.length > 0) {
    const current = queue.shift() as number;
    for (const dep of dependencies) {
      if (dep.predecessorTaskId === current && dep.successorTaskId !== taskId && !reached.has(dep.successorTaskId)) {
        reached.add(dep.successorTaskId);
        queue.push(dep.successorTaskId);
      }
    }
  }
  return reached;
};

/**
 * How many tasks the downstream shift would move: the reachable ones with a
 * date to move, since the server leaves a task with neither date alone. A
 * task `tasks` does not hold is not counted.
 */
export const datedDownstreamCount = (
  taskId: number,
  dependencies: TaskDependency[],
  tasks: (TaskDates & { id: number })[],
): number => {
  const byId = new Map(tasks.map((task) => [task.id, task]));
  return [...downstreamTaskIds(taskId, dependencies)].filter((id) => {
    const task = byId.get(id);
    return Boolean(task?.startDate || task?.dueDate);
  }).length;
};

/** A task tree as one list, parents followed by their subtasks. */
export const flattenTree = (tasks: Task[]): Task[] => tasks.flatMap((task) => [task, ...(task.subtasks ?? [])]);

/** The ISO 8601 week number of a UTC epoch day: weeks start on Monday, week 1 holds the year's first Thursday. */
export const isoWeek = (epochDays: number): number => {
  // Epoch day 0 was a Thursday; ISO weekdays run Monday = 1 … Sunday = 7.
  const weekday = ((((epochDays + 3) % 7) + 7) % 7) + 1;
  const thursday = epochDays - weekday + 4;
  const year = new Date(thursday * MS_PER_DAY).getUTCFullYear();
  const jan1 = Math.floor(Date.UTC(year, 0, 1) / MS_PER_DAY);
  return Math.floor((thursday - jan1) / 7) + 1;
};

/** One header cell: the days `offset` to `offset + length` of the range, starting on epoch day `firstDay`. */
export interface HeaderSegment {
  key: string;
  firstDay: number;
  offset: number;
  length: number;
}

const segmentKey = (epochDays: number, zoom: TimelineZoom): string => {
  if (zoom === "day") return String(epochDays);
  if (zoom === "week") {
    const weekday = (((epochDays + 3) % 7) + 7) % 7; // Monday = 0
    return `w${epochDays - weekday}`;
  }
  return utcDaysToDateStr(epochDays).slice(0, 7);
};

/** The header's cells at a scale: a day each, a Monday-to-Sunday week each, or a calendar month each. */
export const headerSegments = (startDay: number, totalDays: number, zoom: TimelineZoom): HeaderSegment[] => {
  const segments: HeaderSegment[] = [];
  for (let i = 0; i < totalDays; i++) {
    const day = startDay + i;
    const key = segmentKey(day, zoom);
    const last = segments[segments.length - 1];
    if (last && last.key === key) last.length += 1;
    else segments.push({ key, firstDay: day, offset: i, length: 1 });
  }
  return segments;
};
