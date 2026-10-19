/**
 * The two time zones a screen needs (time zones design D4, D6, D8), held for
 * the whole app the way the language preference is:
 *
 * - `timeZone`, the viewer's effective zone — their own when they chose one,
 *   else the workspace's. Every instant is shown in it, and a time a person
 *   types means a moment in it.
 * - `businessTimeZone`, the workspace's. Every bookkeeping day — "today", an
 *   overdue mark, a due date — is judged in it, whoever is looking.
 *
 * The host sets both from the session when it loads (`setTimeZones`); until
 * then, and on a page without a session, both are the browser's own zone.
 */

export type TimeZoneSource = "personal" | "business";

export interface TimeZones {
  /** The zone instants are shown in, and typed times mean: the user's own, else the workspace's. */
  timeZone: string;
  /** Whether `timeZone` is the user's own choice or the workspace's. */
  source: TimeZoneSource;
  /** The workspace's business time zone, the calendar every bookkeeping day is judged in. */
  businessTimeZone: string;
}

/** The browser's own zone, or UTC when it will not say. */
export const browserTimeZone = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
};

const browserZones = (): TimeZones => {
  const zone = browserTimeZone();
  return { timeZone: zone, source: "business", businessTimeZone: zone };
};

/** Whether this browser can format in the zone; a name it does not know would throw on every format. */
const usable = (zone: string | null | undefined): zone is string => {
  if (!zone) return false;
  try {
    new Intl.DateTimeFormat("en", { timeZone: zone });
    return true;
  } catch {
    return false;
  }
};

let zones: TimeZones = browserZones();
const listeners = new Set<() => void>();

export const getTimeZones = (): TimeZones => zones;
export const subscribeToTimeZones = (listener: () => void) => {
  listeners.add(listener);
  return () => listeners.delete(listener);
};

/**
 * Sets the zones from the session's `timeZone`, `timeZoneSource` and
 * `businessTimeZone`; `null` (no session) puts both back on the browser's
 * zone. A name this browser cannot format in falls back the same way, rather
 * than breaking every date on the page.
 */
export const setTimeZones = (
  next: { timeZone?: string | null; timeZoneSource?: TimeZoneSource | null; businessTimeZone?: string | null } | null,
): TimeZones => {
  const fallback = browserTimeZone();
  const business = usable(next?.businessTimeZone) ? next.businessTimeZone : fallback;
  const personal = usable(next?.timeZone) ? next.timeZone : business;
  const source: TimeZoneSource = next?.timeZoneSource === "personal" && usable(next.timeZone) ? "personal" : "business";
  if (zones.timeZone !== personal || zones.businessTimeZone !== business || zones.source !== source) {
    zones = { timeZone: personal, source, businessTimeZone: business };
    for (const listener of listeners) listener();
  }
  return zones;
};

const pad = (value: number) => String(value).padStart(2, "0");
const DAY_MS = 24 * 60 * 60 * 1000;

/** The calendar date and wall-clock time an instant shows in a zone. */
const partsIn = (zone: string, at: Date) => {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: zone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).formatToParts(at);
  const part = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((p) => p.type === type)?.value ?? 0);
  return {
    year: part("year"),
    month: part("month"),
    day: part("day"),
    hour: part("hour") % 24,
    minute: part("minute"),
    second: part("second"),
  };
};

/** The `YYYY-MM-DD` an instant falls on in a zone — "today" in it, by default. */
export const civilDateIn = (zone: string, at: Date = new Date()): string => {
  const p = partsIn(zone, at);
  return `${p.year}-${pad(p.month)}-${pad(p.day)}`;
};

/** The `HH:mm` wall-clock time an instant shows in a zone. */
export const wallClockIn = (zone: string, at: Date): string => {
  const p = partsIn(zone, at);
  return `${pad(p.hour)}:${pad(p.minute)}`;
};

/** How far the zone's wall clock is ahead of UTC at an instant, in milliseconds. */
const offsetAt = (zone: string, at: number): number => {
  const p = partsIn(zone, new Date(at));
  return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second) - Math.floor(at / 1000) * 1000;
};

/**
 * The instant a `YYYY-MM-DD` date and an `HH:mm` time mean in a zone — what a
 * person who typed them meant (time zones design D6). A time the clock passes
 * twice in autumn is the first of the two, and a time it skips in spring is
 * moved on by the gap (02:30 on the night the clock jumps from 02:00 to 03:00
 * is 03:30), as `Temporal`'s "compatible" reading does.
 */
export const instantFromWallClock = (date: string, time: string, zone: string): Date => {
  const [y, m, d] = date.split("-").map(Number);
  const [hh, mm] = time.split(":").map(Number);
  const wall = Date.UTC(y, m - 1, d, hh, mm);
  // A zone changes its offset at most once in a day either side, so the
  // offsets a day before and a day after are the only two it can mean.
  const before = wall - offsetAt(zone, wall - DAY_MS);
  const after = wall - offsetAt(zone, wall + DAY_MS);
  const readsBack = (at: number) => at + offsetAt(zone, at) === wall;
  const candidates = [before, after].filter(readsBack).sort((a, b) => a - b);
  return new Date(candidates[0] ?? before);
};
