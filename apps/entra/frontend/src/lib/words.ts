import { type ApiError, ApiValidationError, NotFoundError } from "../api/request";
import { entraCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
export type Translate = (key: string, values?: Record<string, unknown>) => string;

const catalog = entraCatalog.en as Record<string, string>;
const known = (key: string) => key in catalog;

/** The `code` an `EntraProblem` carries, when it carries one. */
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
  if (error instanceof NotFoundError) return t("refusal.not_found");
  if (error instanceof ApiValidationError) return Object.values(error.fieldErrors)[0] ?? error.message;
  return error instanceof Error ? error.message : String(error);
};

/**
 * The server's field errors for the fields a form shows, and what is left
 * over, which is said out loud rather than dropped.
 */
export const fieldRefusals = (
  error: unknown,
  shown: readonly string[],
  t: Translate,
): { fields: Record<string, string>; rest: string[] } => {
  if (!(error instanceof ApiValidationError)) return { fields: {}, rest: [refusalText(error, t)] };
  const fields: Record<string, string> = {};
  const rest: string[] = [];
  for (const [field, message] of Object.entries(error.fieldErrors)) {
    if (shown.includes(field)) fields[field] = message;
    else rest.push(message);
  }
  if (Object.keys(fields).length === 0 && rest.length === 0) rest.push(error.message);
  return { fields, rest };
};

/** A status reason (`credential_invalid`, `secret_expiring`, …) in words; an unknown one as it came. */
export const reasonText = (reason: string, t: Translate): string =>
  known(`reason.${reason}`) ? t(`reason.${reason}`) : reason;

/** An event's action (`entra.user.activated`) in words; an unknown one as it came. */
export const actionText = (action: string, t: Translate): string =>
  known(`action.${action}`) ? t(`action.${action}`) : action;

/**
 * A reported code in words; `licence_unavailable:ENTERPRISEPACK` names the
 * part number, and an event's refusal (`refused:adoption_unconfirmed`) is
 * worded when the catalogue has it.
 */
export const codeText = (code: string, t: Translate): string => {
  const [name, ...rest] = code.split(":");
  if (name === "refused" && known(`code.refused_${rest.join(":")}`)) return t(`code.refused_${rest.join(":")}`);
  // A code that also comes with a detail (failed:<n>) has its own words alone.
  if (rest.length === 0 && known(`code.${name}.alone`)) return t(`code.${name}.alone`);
  return known(`code.${name}`) ? t(`code.${name}`, { detail: rest.join(":") }) : code;
};

/** Why a connect failed, from the callback's `code` or a refusal's: its own sentence, else the generic failure. */
export const outlookFailedText = (code: string | undefined, t: Translate): string =>
  t(code && known(`outlook.failed_${code}`) ? `outlook.failed_${code}` : "outlook.failed_failed");

/** Why Microsoft refused a connection (`errorCode`), in words; an unknown one as Microsoft asking to sign in again. */
export const outlookReasonText = (code: string | undefined, t: Translate): string =>
  t(code && known(`outlook.reason_${code}`) ? `outlook.reason_${code}` : "outlook.reason_sign_in_again");

/** A feature the granted scopes allow (`calendar`), in words; an unknown one as it came. */
export const outlookFeatureText = (feature: string, t: Translate): string =>
  known(`outlook.feature_${feature}`) ? t(`outlook.feature_${feature}`) : feature;
