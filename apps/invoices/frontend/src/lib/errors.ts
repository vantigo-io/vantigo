import { ApiValidationError } from "../api/request";
import { invoicesCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

/**
 * The refusal's code, when the error is one of this module's 409s or its 503
 * (D2-D8). The shared client puts a problem's top-level `code` on the error
 * itself, a conflict and a generic error alike.
 */
export const refusalCode = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** A 409's own fields — the dates, the line — from its parsed body. */
export const refusalProblem = (error: unknown): Record<string, unknown> =>
  ((error as { problem?: Record<string, unknown> } | null)?.problem ?? {}) as Record<string, unknown>;

/**
 * What a refusal says to a person. A coded refusal is worded by its code, in
 * the reader's language, never the server's English detail — with what the
 * refusal names put into the words: the dates an issue may take
 * (`{{dates}}`, each written by `date`), the line (`{{line}}`) and the
 * customer a merged one went into (`{{mergedInto}}`). A validation error says
 * its first field's message; anything else, the error's own — a code this
 * catalog has no words for included, which is looked up before it is
 * translated: outside production a missing key throws rather than echoing
 * itself back.
 */
export const refusalMessage = (error: unknown, t: Translate, date: (day: string) => string = (d) => d): string => {
  const code = refusalCode(error);
  const key = `refusal.${code}`;
  if (code && key in invoicesCatalog.en) {
    const problem = refusalProblem(error);
    const dates = Array.isArray(problem.allowedIssueDates) ? (problem.allowedIssueDates as string[]) : [];
    return t(key, {
      dates: dates.map(date).join(", "),
      line: problem.linePosition ?? "",
      mergedInto: problem.mergedInto ?? "",
    });
  }
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.errors)[0];
    if (first?.[0]) return first[0];
  }
  return error instanceof Error ? error.message : String(error);
};
