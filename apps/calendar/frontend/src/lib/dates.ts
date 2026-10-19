/** The `YYYY-MM-DD` `days` after `date`, on the calendar alone. */
export const addDays = (date: string, days: number): string => {
  const [y, m, d] = date.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10);
};

/** How many days `to` is after `from`, both `YYYY-MM-DD`. */
export const daysBetween = (from: string, to: string): number =>
  Math.round((Date.parse(`${to}T00:00:00Z`) - Date.parse(`${from}T00:00:00Z`)) / 86_400_000);

/** The day of the week of a `YYYY-MM-DD`, 0 for Sunday as `Date` counts. */
export const weekdayOf = (date: string): number => new Date(`${date}T00:00:00Z`).getUTCDay();

/** A civil date as an instant at UTC midnight, for formatting with `timeZone: "UTC"` so it never shifts. */
export const civilDateValue = (date: string): Date => new Date(`${date}T00:00:00Z`);

export const isCivilDate = (value: unknown): value is string =>
  typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(Date.parse(`${value}T00:00:00Z`));
