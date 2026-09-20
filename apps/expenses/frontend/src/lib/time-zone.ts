/**
 * The installation's business time zone, and the arithmetic a travel claim
 * needs around it.
 *
 * A claim stores two instants. The traveller types a wall-clock time — "we
 * left at 07:00" — and what that instant *is* depends on the zone the company
 * keeps its calendar in, which `GET /meta` answers as `timeZone`. The
 * browser's own zone is never it: somebody on holiday, a laptop left on the
 * wrong setting, or simply a company that is not where its employee is, and
 * the form would send an instant an hour or two away from the one meant — and
 * then disagree with the server about which day a per diem line falls on.
 *
 * So: a wall-clock time is turned into an instant using the offset the
 * *installation's* zone was at that moment (`instantInZone`), and a stored
 * instant is read back as the clock the traveller looked at
 * (`wallClockInZone`). Nothing here uses `Date`'s own local-time accessors.
 */

const pad = (value: number): string => String(value).padStart(2, "0");

const formatters = new Map<string, Intl.DateTimeFormat>();

/**
 * A formatter that writes an instant's calendar fields in `zone`. Built once
 * per zone, because `Intl.DateTimeFormat` is expensive and every row of a per
 * diem table would otherwise build its own.
 *
 * An unknown zone name throws; the caller falls back to the browser's own
 * zone rather than failing the render, and the server refuses the save with a
 * sentence about the setting if it really is wrong.
 */
const fieldsIn = (zone: string, instant: Date) => {
  let formatter = formatters.get(zone);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat("en-US", {
      timeZone: zone,
      hourCycle: "h23",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
    formatters.set(zone, formatter);
  }
  const parts = formatter.formatToParts(instant);
  const field = (type: Intl.DateTimeFormatPartTypes): number =>
    Number(parts.find((part) => part.type === type)?.value ?? 0);
  return {
    year: field("year"),
    month: field("month"),
    day: field("day"),
    hour: field("hour"),
    minute: field("minute"),
    second: field("second"),
  };
};

/**
 * Whether this browser can do arithmetic in `zone` at all.
 *
 * Go and Postgres agree on the installation's zone before it is stored, but a
 * traveller's older phone may not know a name either of them does — a recent
 * rename (`Europe/Kyiv`, `America/Ciudad_Juarez`), or a tzdata release the
 * device never got. **Reading** an instant in a zone it does not know falls
 * back to the reader's own, which is a cosmetic wrong; **writing** one would
 * capture the phone's offset under the installation's label and save the trip
 * hours off with no refusal. A form asks this first and declines to convert.
 */
export const isKnownZone = (zone: string | undefined): zone is string => {
  if (!zone) return false;
  try {
    fieldsIn(zone, new Date(0));
    return true;
  } catch {
    formatters.delete(zone);
    return false;
  }
};

const knownZone = (zone: string | undefined): string =>
  isKnownZone(zone) ? zone : Intl.DateTimeFormat().resolvedOptions().timeZone;

/**
 * How many minutes east of UTC `zone` stood at that instant. It is derived,
 * not tabulated: the zone's own calendar fields for the instant, read back as
 * if they were UTC, differ from the instant by exactly its offset — which is
 * what makes it right on the day the clocks move.
 */
export const zoneOffsetMinutes = (zone: string, instant: Date): number => {
  const fields = fieldsIn(knownZone(zone), instant);
  const asIfUtc = Date.UTC(fields.year, fields.month - 1, fields.day, fields.hour, fields.minute, fields.second);
  return Math.round((asIfUtc - (instant.getTime() - instant.getMilliseconds())) / 60_000);
};

const offsetLabel = (minutes: number): string => {
  const sign = minutes < 0 ? "-" : "+";
  const absolute = Math.abs(minutes);
  return `${sign}${pad(Math.floor(absolute / 60))}:${pad(absolute % 60)}`;
};

const WALL_CLOCK_TIME = /^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$/;

/**
 * Whether a value is a clock time a form may act on. A time field is empty
 * for a moment while somebody retypes it, and half a time is not a time: a
 * form asks this before it works an instant out, so nothing has to guess.
 */
export const isWallClockTime = (value: unknown): value is string =>
  typeof value === "string" && WALL_CLOCK_TIME.test(value);

/**
 * The instant a wall-clock day and time name in `zone`, as an ISO string
 * carrying that zone's offset — `2026-03-09T07:00:00+01:00` for an Oslo
 * departure in winter and `+02:00` for the same clock time in summer.
 *
 * The offset is resolved twice because it depends on the very instant being
 * computed: the first pass guesses with the offset at the wall clock read as
 * UTC, the second asks again at the instant that produced. That settles every
 * date except the hour a zone skips or repeats, where either answer names a
 * real instant and the traveller sees which one in the field beside it.
 */
export const instantInZone = (date: string, time: string, zone: string): string => {
  const safeZone = knownZone(zone);
  const [year, month, day] = date.split("-").map(Number);
  const [hour, minute] = time.split(":").map(Number);
  const wallClock = Date.UTC(year, month - 1, day, hour, minute, 0);
  // A day or a time that is not one yet — a field being retyped — has no
  // instant, and the caller's own validation is what says so. Returning the
  // empty string keeps a render from throwing on a half-typed field.
  if (!Number.isFinite(wallClock)) return "";
  const guessed = zoneOffsetMinutes(safeZone, new Date(wallClock));
  const settled = zoneOffsetMinutes(safeZone, new Date(wallClock - guessed * 60_000));
  return `${date}T${pad(hour)}:${pad(minute)}:00${offsetLabel(settled)}`;
};

/** A stored instant as the day and clock time somebody in `zone` reads off it. */
export const wallClockInZone = (instant: string, zone: string): { date: string; time: string } => {
  const fields = fieldsIn(knownZone(zone), new Date(instant));
  return {
    date: `${fields.year}-${pad(fields.month)}-${pad(fields.day)}`,
    time: `${pad(fields.hour)}:${pad(fields.minute)}`,
  };
};

/**
 * The calendar day a stored instant falls on in `zone` — the day the period
 * lock judges a trip on, the day `GET /claims`' filters match it under, and
 * the first and last day one of its per diem lines may be dated.
 */
export const zoneCalendarDate = (instant: string, zone: string): string => wallClockInZone(instant, zone).date;
