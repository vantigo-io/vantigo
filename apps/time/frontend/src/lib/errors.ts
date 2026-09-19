import { ApiValidationError } from "../api/request";

/**
 * What to tell the person when the server refused a write. A validation
 * refusal names the field it is about — the day cap, the lock and a
 * mismatched duration all arrive on `hours` — and that sentence says more
 * than the problem's title does, so it wins.
 */
export const refusalMessage = (error: Error, preferredField = "hours"): string => {
  if (error instanceof ApiValidationError) {
    const messages = error.fieldErrors;
    return messages[preferredField] ?? Object.values(messages)[0] ?? error.message;
  }
  return error.message;
};

/**
 * Every sentence a refusal carries about one field. An approval, a rejection
 * and an unapproval are all or nothing, and each answers one message per
 * offending id on `ids` — the caller is shown all of them, not just the first.
 */
export const refusalMessages = (error: Error, field = "ids"): string[] => {
  if (error instanceof ApiValidationError) {
    const messages = error.fields[field] ?? Object.values(error.fields).flat();
    if (messages.length > 0) return messages;
  }
  return [error.message];
};
