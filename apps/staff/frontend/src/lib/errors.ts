import { ApiConflictError, ApiValidationError } from "../api/request";

/**
 * Whether a replace was refused because the row moved on since it was read
 * (`revision_conflict`). The server's sentence names revision numbers, which
 * mean nothing to the person; the page reloads and says so in their words.
 */
export const isRevisionConflict = (error: unknown): boolean =>
  error instanceof ApiConflictError && error.code === "revision_conflict";

/**
 * What to tell the person when the server refused a write. A validation
 * refusal names the field it is about, and that sentence says more than the
 * problem's title does, so it wins; a 409 (`structure_default`,
 * `revision_conflict` …) carries its sentence as the problem's detail, which
 * the client has already made the message.
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
 * The server's field errors for the fields a form shows, keyed as the form
 * keys them, and what is left over — a field the form does not show still has
 * to be said out loud rather than dropped. The module keys a nested field as
 * `terms.ftePercent`, which is exactly the path a Mantine form uses for a
 * nested value, so a form whose values mirror the request needs no mapping.
 */
export const fieldRefusals = (
  error: Error,
  shown: readonly string[],
): { fields: Record<string, string>; rest: string[] } => {
  if (!(error instanceof ApiValidationError)) return { fields: {}, rest: [error.message] };
  const fields: Record<string, string> = {};
  const rest: string[] = [];
  for (const [field, message] of Object.entries(error.fieldErrors)) {
    if (shown.includes(field)) fields[field] = message;
    else rest.push(message);
  }
  if (Object.keys(fields).length === 0 && rest.length === 0) rest.push(error.message);
  return { fields, rest };
};

/**
 * Where a 409 refusal belongs on a form. A conflict carries its code and a
 * sentence but no field, since the rule is about the row among others (a day
 * another row has, a manager graph that would loop); the form still owes the
 * reader the place it is about. `fields` maps a code to the form's path;
 * a code it does not name is said as a message instead.
 */
export const conflictField = (error: unknown, fields: Record<string, string>): Record<string, string> | undefined => {
  if (!(error instanceof ApiConflictError) || !error.code || !fields[error.code]) return undefined;
  return { [fields[error.code]]: error.message };
};

/** The terms rules that are refused as a 409, and the field of a terms form each is about. */
export const termsConflictFields: Record<string, string> = {
  terms_valid_from_taken: "validFrom",
  terms_before_start: "validFrom",
  terms_after_end: "validFrom",
  terms_first_row_start: "validFrom",
  self_manager: "terms.managerPersonId",
  manager_cycle: "terms.managerPersonId",
};

/**
 * The server's field errors placed by `place`, which turns the request's
 * field (`nextOfKin[1].name`) into the form's path (`nextOfKin.1.name`), or
 * says the form does not show it — then the sentence is said out loud.
 */
export const mappedRefusals = (
  error: Error,
  place: (field: string) => string | undefined,
): { fields: Record<string, string>; rest: string[] } => {
  if (!(error instanceof ApiValidationError)) return { fields: {}, rest: [error.message] };
  const fields: Record<string, string> = {};
  const rest: string[] = [];
  for (const [field, message] of Object.entries(error.fieldErrors)) {
    const path = place(field);
    if (path) fields[path] = message;
    else rest.push(message);
  }
  if (Object.keys(fields).length === 0 && rest.length === 0) rest.push(error.message);
  return { fields, rest };
};

/** A list element's field as the API names it (`roles[0].toDate`), as a Mantine form path (`roles.0.toDate`). */
export const listPath = (field: string): string => field.replace(/\[(\d+)\]/g, ".$1");
