import type { LocaleFormatters } from "@vantigo/frontend-shell";

/**
 * A date-only value (`yyyy-MM-dd`) written the way the reader's locale writes
 * dates.
 *
 * The `T00:00:00Z` and the `timeZone: "UTC"` go together, and neither is
 * optional: this API's date-only fields — an entry's `occurredOn`, a follow-up's
 * `dueOn` — name a calendar day with no zone at all, and rendering one in the
 * browser's zone moves it a day for every reader west of Greenwich. A `doneAt`
 * timestamp is shown through here too, with its date sliced off first, so the
 * day it names is the day the server filed it under.
 */
export const formatDateOnly = (formatters: LocaleFormatters, date: string) =>
  formatters.formatDate(`${date}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" });
