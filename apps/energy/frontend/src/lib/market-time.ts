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
