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
