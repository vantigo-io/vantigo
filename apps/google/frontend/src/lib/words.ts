import { type ApiError, ApiValidationError, NotFoundError } from "../api/request";
import { googleCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
export type Translate = (key: string, values?: Record<string, unknown>) => string;

const catalog = googleCatalog.en as Record<string, string>;
const known = (key: string) => key in catalog;

/** The `code` a `GoogleProblem` carries, when it carries one. */
export const codeOf = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** The connection was written since the version the form holds (412 `version_mismatch`). */
export const isStale = (error: unknown): boolean =>
  (error as ApiError | undefined)?.status === 412 || codeOf(error) === "version_mismatch";

/**
 * A refusal in the reader's words: a coded one by its code
 * (`refusal.<code>`), never the server's English; a validation error by its
 * first field's sentence; anything else by its own message.
 */
export const refusalText = (error: unknown, t: Translate): string => {
  const code = codeOf(error);
  if (code && known(`refusal.${code}`)) return t(`refusal.${code}`);
  if ((error as ApiError | undefined)?.status === 412) return t("refusal.version_mismatch");
  if ((error as ApiError | undefined)?.status === 429) return t("refusal.too_many_requests");
  if (error instanceof NotFoundError) return t("refusal.not_found");
  if (error instanceof ApiValidationError) return Object.values(error.fieldErrors)[0] ?? error.message;
  return error instanceof Error ? error.message : String(error);
};

/**
 * The server's field errors for the fields a form shows, and what is left
 * over, which is said out loud rather than dropped. A coded refusal is
 * always left over, in words.
 */
export const fieldRefusals = (
  error: unknown,
  shown: readonly string[],
  t: Translate,
): { fields: Record<string, string>; rest: string[] } => {
  if (!(error instanceof ApiValidationError) || codeOf(error)) return { fields: {}, rest: [refusalText(error, t)] };
  const fields: Record<string, string> = {};
  const rest: string[] = [];
  for (const [field, message] of Object.entries(error.fieldErrors)) {
    if (shown.includes(field)) fields[field] = message;
    else rest.push(message);
  }
  if (Object.keys(fields).length === 0 && rest.length === 0) rest.push(error.message);
  return { fields, rest };
};

/** A status reason (`not_delegated`, `credential_invalid`, …) in words; an unknown one as it came. */
export const reasonText = (reason: string, t: Translate): string =>
  known(`reason.${reason}`) ? t(`reason.${reason}`) : reason;

/** An event's action (`google.connection.tested`) in words; an unknown one as it came. */
export const actionText = (action: string, t: Translate): string =>
  known(`action.${action}`) ? t(`action.${action}`) : action;

/** An event's code in words when the catalogue has it (a status or a reason); otherwise as it came. */
export const codeText = (code: string, t: Translate): string => {
  if (known(`statusName.${code}`)) return t(`statusName.${code}`);
  if (known(`code.${code}`)) return t(`code.${code}`);
  return code;
};

/**
 * What the module could not act on for an account (`privileged`,
 * `licence_unavailable:<sku>`, `leaver_step_failed:<step>`, …) in words; the
 * part after the colon is the detail. A code with no detail has its own words
 * (`reported.<code>.alone`) when the catalogue has them; an unknown code is
 * shown as it came.
 */
export const reportedText = (code: string, t: Translate): string => {
  const [name, ...rest] = code.split(":");
  const detail = rest.join(":");
  if (!detail && known(`reported.${name}.alone`)) return t(`reported.${name}.alone`);
  return known(`reported.${name}`) ? t(`reported.${name}`, { detail }) : code;
};

/** An account's last refusal (`privileged`, `ambiguous`, `address_taken`, `enabled`) in words; an unknown one as it came. */
export const lastErrorText = (code: string, t: Translate): string =>
  t("lastError", { reason: known(`lastError.${code}`) ? t(`lastError.${code}`) : code });
