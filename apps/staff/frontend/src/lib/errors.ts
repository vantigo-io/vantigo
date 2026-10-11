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
