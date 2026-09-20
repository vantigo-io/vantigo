import { ApiValidationError } from "../api/request";

/**
 * What to tell the person when the server refused a write. A validation
 * refusal names the field it is about, and that sentence says more than the
 * problem's title does, so it wins.
 */
export const refusalMessage = (error: Error, preferredField?: string): string => {
  if (error instanceof ApiValidationError) {
    const messages = error.fieldErrors;
    if (preferredField && messages[preferredField]) return messages[preferredField];
    return Object.values(messages)[0] ?? error.message;
  }
  return error.message;
};

/**
 * Every sentence a batch refusal carried. Submitting is all or nothing and
 * answers one message per offending id on `entryIds`, so the caller is shown
 * all of them rather than the first — each one names the expense it is about.
 */
export const refusalMessages = (error: Error, field = "entryIds"): string[] => {
  if (error instanceof ApiValidationError) {
    const messages = error.fields[field] ?? Object.values(error.fields).flat();
    if (messages.length > 0) return messages;
  }
  return [error.message];
};

/**
 * The batch refusals keyed by the expense each one is about. Every per-id
 * message the module writes opens with `Expense N …` (Task 4's report, "the
 * per-id refusal shape"), which is what lets the list put the sentence
 * against its own row instead of piling them all into one notification.
 * Anything that does not name an id is returned under `rest` and said out
 * loud — a cap, a missing body, a title with no field errors at all.
 */
export interface RefusalsByEntry {
  byEntry: Map<number, string[]>;
  rest: string[];
}

export const refusalsByEntry = (error: Error, field = "entryIds"): RefusalsByEntry => {
  const byEntry = new Map<number, string[]>();
  const rest: string[] = [];
  for (const message of refusalMessages(error, field)) {
    const named = /^Expense (\d+)\b/.exec(message);
    if (!named) {
      rest.push(message);
      continue;
    }
    const id = Number(named[1]);
    byEntry.set(id, [...(byEntry.get(id) ?? []), message]);
  }
  return { byEntry, rest };
};
