/**
 * Conversions between Mantine date/time picker strings and the API's ISO 8601
 * UTC timestamps. Pickers work in the browser's local timezone — campaigns are
 * planned in local wall-clock time — while the API stores and returns UTC.
 */

/** Converts a picker value ("YYYY-MM-DD" or "YYYY-MM-DD HH:mm[:ss]", local time) to a UTC ISO timestamp. */
export const toIsoTimestamp = (value: string | null) => {
  if (!value) return undefined;
  const normalized = value.includes(" ") ? value.replace(" ", "T") : `${value}T00:00:00`;
  return new Date(normalized).toISOString();
};

/** Converts an API ISO timestamp to a local "YYYY-MM-DD HH:mm:ss" picker value. */
export const toPickerValue = (value: string | null) => {
  if (!value) return null;
  const date = new Date(value);
  const pad = (part: number) => String(part).padStart(2, "0");
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  );
};
