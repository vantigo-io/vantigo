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
