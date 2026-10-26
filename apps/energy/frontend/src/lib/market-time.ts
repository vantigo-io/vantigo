export const MARKET_TIME_ZONE = "Europe/Oslo";

const osloDayFormatter = new Intl.DateTimeFormat("en-CA", {
  timeZone: MARKET_TIME_ZONE,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});

/** Returns a stable local-day key without relying on the browser's time zone. */
export const marketDayKey = (value: string): string =>
  osloDayFormatter
    .formatToParts(new Date(value))
    .filter((part) => part.type === "year" || part.type === "month" || part.type === "day")
    .map((part) => part.value)
    .join("-");

const osloClockFormatter = new Intl.DateTimeFormat("en-CA", {
  timeZone: MARKET_TIME_ZONE,
  hourCycle: "h23",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});

/** The market's clock at `at`, read as if it were UTC: the difference is the offset. */
const osloWallClock = (at: number): number => {
  const part = (type: Intl.DateTimeFormatPartTypes) =>
    Number(osloClockFormatter.formatToParts(at).find((item) => item.type === type)?.value);
  return Date.UTC(part("year"), part("month") - 1, part("day"), part("hour"), part("minute"), part("second"));
};

/**
 * The market's offset from UTC at `value`, as `+01` or `+02` (`+05:30` were
 * the offset not whole hours): what tells the two runs of the hour apart on
 * the day the clock goes back.
 */
export const marketUtcOffset = (value: string): string => {
  const at = new Date(value).getTime();
  const minutes = Math.round((osloWallClock(at) - at) / 60_000);
  const sign = minutes < 0 ? "-" : "+";
  const hours = String(Math.floor(Math.abs(minutes) / 60)).padStart(2, "0");
  const rest = Math.abs(minutes) % 60;
  return `${sign}${hours}${rest ? `:${String(rest).padStart(2, "0")}` : ""}`;
};

/**
 * The instant a civil day (`YYYY-MM-DD`) starts in the market's time zone,
 * as an ISO timestamp — whatever zone the browser is in. Oslo never changes
 * its clock at midnight, so the offset at the guess is the day's.
 */
export const marketDayStart = (day: string): string => {
  const [year, month, date] = day.split("-").map(Number);
  const guess = Date.UTC(year, month - 1, date);
  const start = guess - (osloWallClock(guess) - guess);
  return new Date(start - (osloWallClock(start) - guess)).toISOString();
};

/** The civil day `days` after `day`, both `YYYY-MM-DD`. */
export const addMarketDays = (day: string, days: number): string => {
  const [year, month, date] = day.split("-").map(Number);
  return new Date(Date.UTC(year, month - 1, date + days)).toISOString().slice(0, 10);
};

/** The first civil day of the month after `day`'s, `YYYY-MM-DD`. */
export const nextMarketMonth = (day: string): string => {
  const [year, month] = day.split("-").map(Number);
  return new Date(Date.UTC(year, month, 1)).toISOString().slice(0, 10);
};

/** The civil day `months` months after `day`'s month starts, `YYYY-MM-01`. */
export const addMarketMonths = (day: string, months: number): string => {
  const [year, month] = day.split("-").map(Number);
  return new Date(Date.UTC(year, month - 1 + months, 1)).toISOString().slice(0, 10);
};

/** The Monday that starts the ISO week holding `day`, `YYYY-MM-DD`. */
export const marketWeekStart = (day: string): string => {
  const [year, month, date] = day.split("-").map(Number);
  const weekday = new Date(Date.UTC(year, month - 1, date)).getUTCDay();
  return addMarketDays(day, -((weekday + 6) % 7));
};

/** The ISO week of `day` (`YYYY-MM-DD`) and the year it belongs to, which near New Year may be the next or the last. */
export const isoWeek = (day: string): { year: number; week: number } => {
  // The week belongs to the year its Thursday is in.
  const thursday = addMarketDays(marketWeekStart(day), 3);
  const year = Number(thursday.slice(0, 4));
  const ordinal = (Date.parse(thursday) - Date.UTC(year, 0, 1)) / 86_400_000;
  return { year, week: Math.floor(ordinal / 7) + 1 };
};
