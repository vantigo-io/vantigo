import { ApiValidationError } from "../api/request";
import { invoicesCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * The refusal's code: a problem's `code` (this module's 409s, its 502 and its
 * 503s) or the rate limiter's `error.code` (its 429). The shared client puts
 * either on the error itself, whatever the status — a conflict and a generic
 * error alike.
 */
export const refusalCode = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** A refusal's own fields — the dates, the line, the network's answer, the rules — from its parsed body. */
export const refusalProblem = (error: unknown): Record<string, unknown> =>
  ((error as { problem?: Record<string, unknown> } | null)?.problem ?? {}) as Record<string, unknown>;

/**
 * What a refusal says to a person. A coded refusal is worded by its code, in
 * the reader's language, never the server's English detail — with what the
 * refusal names put into the words: the dates an issue may take
 * (`{{dates}}`, each written by `date`), the line (`{{line}}`), the
 * customer a merged one went into (`{{mergedInto}}`) and the open amount a
 * payment exceeded (`{{openAmount}}`, written by `money`). The rate limiter's
 * `rate_limited` (its 429, not a problem document) is worded by
 * `refusal.rateLimited`. A validation error says
 * its first field's message; anything else, the error's own — a code this
 * catalog has no words for included, which is looked up before it is
 * translated: outside production a missing key throws rather than echoing
 * itself back.
 */
export const refusalMessage = (
  error: unknown,
  t: Translate,
  date: (day: string) => string = (d) => d,
  money: (amount: number) => string = (amount) => String(amount),
): string => {
  const code = refusalCode(error);
  const key = code === "rate_limited" ? "refusal.rateLimited" : `refusal.${code}`;
  if (code && key in invoicesCatalog.en) {
    const problem = refusalProblem(error);
    const dates = Array.isArray(problem.allowedIssueDates) ? (problem.allowedIssueDates as string[]) : [];
    return t(key, {
      dates: dates.map(date).join(", "),
      line: problem.linePosition ?? "",
      mergedInto: problem.mergedInto ?? "",
      openAmount: typeof problem.openAmount === "number" ? money(problem.openAmount) : "",
    });
  }
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.errors)[0];
    if (first?.[0]) return first[0];
  }
  return error instanceof Error ? error.message : String(error);
};

/**
 * A refusal code in words, without an error to carry it — a `blockedBy` the
 * server judged ahead of the request: the catalog's `refusal.<code>`, or the
 * code itself when this version has no words for it.
 */
export const refusalWords = (code: string, t: Translate): string =>
  `refusal.${code}` in invoicesCatalog.en ? t(`refusal.${code}`) : code;

/**
 * A 400's refusals split by where they are shown: each field an input on
 * screen shows (`hasInput`), and every other one for a notification — a field
 * whose input is not rendered just now is never swallowed. Each is worded by
 * the catalog's `fieldInvalid.<field>` — a line's `lines[2].quantity` by
 * `fieldInvalid.line.quantity`, and a field of another request than the
 * draft's under its `scope`, a payment's `note` by `fieldInvalid.payment.note`
 * — and by the server's own sentence only where
 * the catalog has no words for the field. Each field's
 * catalog sentence covers every rule the server checks on it, so it says what
 * was refused in the reader's language.
 */
export const fieldRefusals = (
  error: ApiValidationError,
  t: Translate,
  hasInput: (field: string) => boolean,
  scope?: string,
): { onInputs: Record<string, string>; elsewhere: string[] } => {
  const onInputs: Record<string, string> = {};
  const elsewhere: string[] = [];
  for (const [field, message] of Object.entries(error.fieldErrors)) {
    const key = `fieldInvalid.${scope ? `${scope}.` : ""}${field.replace(/^lines\[\d+\]\./, "line.")}`;
    const words = key in invoicesCatalog.en ? t(key) : message;
    if (hasInput(field)) onInputs[field] = words;
    else elsewhere.push(words);
  }
  return { onInputs, elsewhere };
};

/**
 * A draft's warning in words. A warning this catalog has no words for — one
 * a newer server added — is looked up before it is translated, as a refusal's
 * code is: outside production a missing key throws, and a warning must never
 * take the editor down with it.
 */
export const warningMessage = (warning: string, t: Translate): string => {
  const key = `warning.${warning}`;
  return key in invoicesCatalog.en ? t(key) : t("warningUnknown", { code: warning });
};
