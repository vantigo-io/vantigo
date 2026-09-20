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
 * The two lists every batch operation refuses on. A batch moves two kinds of
 * unit — standalone expenses and travel claims — and answers one per-id
 * sentence per offending id on the list that named it. Reading only the first
 * drops a trip's explanation on the floor and shows the caller the generic
 * failure instead.
 */
export const flowRefusalFields = ["entryIds", "claimIds"] as const;

const fieldList = (fields: string | readonly string[]): readonly string[] =>
  typeof fields === "string" ? [fields] : fields;

/**
 * Every sentence a batch refusal carried, in the order the lists are given.
 * Submitting is all or nothing and answers one message per offending id, so
 * the caller is shown all of them rather than the first — each one names the
 * expense or the travel claim it is about. A refusal that named neither list
 * (a cap, a missing body) is still returned, because something has to be said.
 */
export const refusalMessages = (error: Error, fields: string | readonly string[] = flowRefusalFields): string[] => {
  if (error instanceof ApiValidationError) {
    const named = fieldList(fields).flatMap((field) => error.fields[field] ?? []);
    if (named.length > 0) return named;
    const anything = Object.values(error.fields).flat();
    if (anything.length > 0) return anything;
  }
  return [error.message];
};

/**
 * The batch refusals keyed by the unit each one is about. Every per-id message
 * the module writes opens with `Expense N …` or `Travel claim N …`, which is
 * what lets a list put the sentence against its own row instead of piling them
 * all into one notification. The two units number independently, so an expense
 * 7 and a trip 7 are different rows and are kept apart.
 *
 * Anything that names no unit is returned under `rest` and said out loud — a
 * cap, a missing body, a title with no field errors at all.
 */
export interface RefusalsByUnit {
  byEntry: Map<number, string[]>;
  byClaim: Map<number, string[]>;
  rest: string[];
}

const add = (into: Map<number, string[]>, id: number, message: string) => {
  into.set(id, [...(into.get(id) ?? []), message]);
};

export const refusalsByUnit = (
  error: Error,
  fields: string | readonly string[] = flowRefusalFields,
): RefusalsByUnit => {
  const byEntry = new Map<number, string[]>();
  const byClaim = new Map<number, string[]>();
  const rest: string[] = [];
  for (const message of refusalMessages(error, fields)) {
    const claim = /^Travel claim (\d+)\b/.exec(message);
    if (claim) {
      add(byClaim, Number(claim[1]), message);
      continue;
    }
    const entry = /^Expense (\d+)\b/.exec(message);
    if (entry) {
      add(byEntry, Number(entry[1]), message);
      continue;
    }
    rest.push(message);
  }
  return { byEntry, byClaim, rest };
};

/** What `refusalsByUnit` answers for a caller that only draws expense rows. */
export interface RefusalsByEntry {
  byEntry: Map<number, string[]>;
  rest: string[];
}

/**
 * The same refusals, for a page with nowhere to put a travel claim's sentence:
 * a trip's message goes to `rest` and is said out loud rather than dropped.
 */
export const refusalsByEntry = (
  error: Error,
  fields: string | readonly string[] = flowRefusalFields,
): RefusalsByEntry => {
  const { byEntry, byClaim, rest } = refusalsByUnit(error, fields);
  return { byEntry, rest: [...rest, ...[...byClaim.values()].flat()] };
};

/**
 * The line a travel claim's refusal points at, when it points at one — the
 * submit refuses the whole trip and names the expense that stopped it
 * (`Travel claim 7 cannot be submitted: Expense 12 cannot be priced: …`), and
 * the claim page can put that sentence against the line itself. A sentence
 * about the trip alone names nobody.
 *
 * **The capital is not guaranteed.** The module writes the word at the start
 * of a sentence in some refusals and mid-sentence in others — the unapprove's
 * `Travel claim 7 holds expense 12, which has been invoiced` is the one the
 * drawer exists to place — so the match takes either case. The claim's own
 * key is still decided by `refusalsByUnit`'s **anchored** `^Travel claim`,
 * which no sentence like this can satisfy, so a message never moves off the
 * trip: it is shown in both places.
 */
export const lineNamedIn = (message: string): number | undefined => {
  const named = /\b[Ee]xpense (\d+)\b/.exec(message);
  return named ? Number(named[1]) : undefined;
};
