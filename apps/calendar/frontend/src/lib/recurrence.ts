import { weekdayOf } from "./dates";

export const weekdays = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"] as const;
export type Weekday = (typeof weekdays)[number];

export type RecurrenceFrequency = "none" | "daily" | "weekly" | "monthly";
export type RecurrenceEnd = "none" | "until" | "count";
/** A monthly series on the day of the month (`BYMONTHDAY=21`) or on an ordinal weekday (`BYDAY=3TU`). */
export type MonthlyMode = "day" | "weekday";
/** Which of the month's weekdays: the first to the fourth, or the last (`-1`). */
export type WeekdayOrdinal = 1 | 2 | 3 | 4 | -1;

/**
 * The recurrence control's model of the RRULE subset the API accepts
 * (design D4): FREQ DAILY, WEEKLY or MONTHLY, INTERVAL, plain weekdays for
 * WEEKLY, one day of the month or one ordinal weekday for MONTHLY, and at
 * most one of COUNT and UNTIL. Every field is kept whatever the frequency, so
 * switching back and forth in the control loses nothing.
 */
export interface RecurrenceModel {
  freq: RecurrenceFrequency;
  interval: number;
  weekdays: Weekday[];
  monthlyMode: MonthlyMode;
  monthDay: number;
  ordinal: WeekdayOrdinal;
  ordinalWeekday: Weekday;
  end: RecurrenceEnd;
  /** The last day the series may fall on, `YYYY-MM-DD`. */
  until: string | null;
  count: number;
}

/** `Date`'s Sunday-first index to the RRULE's weekday. */
const weekdayName = (date: string): Weekday => weekdays[(weekdayOf(date) + 6) % 7];

/** A model for a series starting on `startDate`: not repeating, with every other field ready for it. */
export const defaultRecurrence = (startDate: string): RecurrenceModel => {
  const day = Number(startDate.slice(8, 10));
  return {
    freq: "none",
    interval: 1,
    weekdays: [weekdayName(startDate)],
    monthlyMode: "day",
    monthDay: day,
    // The 29th to the 31st are only ever the month's last such weekday.
    ordinal: day > 28 ? -1 : (Math.ceil(day / 7) as WeekdayOrdinal),
    ordinalWeekday: weekdayName(startDate),
    end: "none",
    until: null,
    count: 10,
  };
};

const frequencies: Record<string, RecurrenceFrequency> = { DAILY: "daily", WEEKLY: "weekly", MONTHLY: "monthly" };

const isWeekday = (value: string): value is Weekday => (weekdays as readonly string[]).includes(value);

/** `20261231` or `20261231T225959Z` as the date it starts with. */
const untilDate = (value: string): string | null => {
  const match = /^(\d{4})(\d{2})(\d{2})/.exec(value);
  return match ? `${match[1]}-${match[2]}-${match[3]}` : null;
};

/**
 * Reads a rule into the control's model, on top of `defaultRecurrence(startDate)`.
 * An empty rule is "none"; a part outside the subset is ignored — the server
 * stores only the subset, so a rule it answered always reads back whole.
 */
export const parseRule = (rule: string | null | undefined, startDate: string): RecurrenceModel => {
  const model = defaultRecurrence(startDate);
  if (!rule?.trim()) return model;
  const parts = new Map(
    rule
      .replace(/^RRULE:/i, "")
      .split(";")
      .map((part) => part.split("=") as [string, string | undefined])
      .filter(([key, value]) => key && value !== undefined)
      .map(([key, value]) => [key.trim().toUpperCase(), (value ?? "").trim().toUpperCase()]),
  );
  const freq = frequencies[parts.get("FREQ") ?? ""];
  if (!freq) return model;
  model.freq = freq;
  const interval = Number(parts.get("INTERVAL") ?? "1");
  model.interval = Number.isInteger(interval) && interval >= 1 ? interval : 1;

  const byDay = (parts.get("BYDAY") ?? "").split(",").filter(Boolean);
  if (freq === "weekly") {
    const days = byDay.filter(isWeekday);
    if (days.length > 0) model.weekdays = weekdays.filter((day) => days.includes(day));
  }
  if (freq === "monthly") {
    const monthDay = Number(parts.get("BYMONTHDAY"));
    const ordinal = /^([+-]?\d)(MO|TU|WE|TH|FR|SA|SU)$/.exec(byDay[0] ?? "");
    if (ordinal) {
      const n = Number(ordinal[1]);
      model.monthlyMode = "weekday";
      model.ordinal = (n === -1 || (n >= 1 && n <= 4) ? n : 1) as WeekdayOrdinal;
      model.ordinalWeekday = ordinal[2] as Weekday;
    } else if (Number.isInteger(monthDay) && monthDay >= 1 && monthDay <= 31) {
      model.monthlyMode = "day";
      model.monthDay = monthDay;
    }
  }

  const count = Number(parts.get("COUNT"));
  const until = untilDate(parts.get("UNTIL") ?? "");
  if (Number.isInteger(count) && count >= 1) {
    model.end = "count";
    model.count = count;
  } else if (until) {
    model.end = "until";
    model.until = until;
  }
  return model;
};

/**
 * Writes the model as the RRULE value the API takes, in the order the server
 * stores it — `FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE;COUNT=10` — or undefined
 * when the appointment does not repeat.
 */
export const formatRule = (model: RecurrenceModel): string | undefined => {
  if (model.freq === "none") return undefined;
  const parts = [`FREQ=${model.freq.toUpperCase()}`, `INTERVAL=${Math.max(1, Math.trunc(model.interval) || 1)}`];
  if (model.freq === "weekly" && model.weekdays.length > 0)
    parts.push(`BYDAY=${weekdays.filter((day) => model.weekdays.includes(day)).join(",")}`);
  if (model.freq === "monthly")
    parts.push(
      model.monthlyMode === "weekday"
        ? `BYDAY=${model.ordinal}${model.ordinalWeekday}`
        : `BYMONTHDAY=${model.monthDay}`,
    );
  if (model.end === "count") parts.push(`COUNT=${Math.max(1, Math.trunc(model.count) || 1)}`);
  if (model.end === "until" && model.until) parts.push(`UNTIL=${model.until.replaceAll("-", "")}`);
  return parts.join(";");
};
