const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;

const pad = (value: number): string => String(value).padStart(2, "0");

/**
 * A calendar date is a calendar date: it is read and written in UTC so that a
 * day never slides by one for somebody east or west of the server. Every
 * `entryDate` in this package goes through here.
 */
export const toUtc = (date: string): Date => new Date(`${date}T00:00:00.000Z`);

export const fromUtc = (date: Date): string =>
  `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())}`;

/** Whether the value is a calendar date the API would take — and one that exists. */
export const isIsoDate = (value: unknown): value is string =>
  typeof value === "string" && ISO_DATE.test(value) && fromUtc(toUtc(value)) === value;

/** The caller's calendar date on their own clock, which is the day they spent the money. */
export const today = (): string => {
  const now = new Date();
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
};

/**
 * A calendar date written for the reader. `timeZone: "UTC"` is not a detail:
 * without it `new Date("2026-03-10")` is midnight UTC, which is the 9th in
 * anything west of Greenwich.
 */
export const formatCalendarDate = (
  date: string,
  formatDate: (value: Date, options?: Intl.DateTimeFormatOptions) => string,
): string => formatDate(toUtc(date), { dateStyle: "medium", timeZone: "UTC" });
