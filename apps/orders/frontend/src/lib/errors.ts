import { ApiValidationError, NotFoundError, refusalCode } from "../api/request";
import { ordersCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

/** Whether the catalog has words for a key; outside production a missing key throws rather than echoing itself. */
export const hasWords = (key: string): boolean => key in ordersCatalog.en;

/**
 * What a refusal says to a person (orders design §3). A coded refusal is
 * worded by its code in the reader's language, never the server's English;
 * a field refusal says its first field's message; a 404 and a refusal without
 * words are worded by their status.
 */
export const refusalMessage = (error: unknown, t: Translate): string => {
  const code = refusalCode(error);
  if (code && hasWords(`refusal.${code}`)) return t(`refusal.${code}`);
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.errors)[0];
    if (first?.[0]) return first[0];
  }
  if (error instanceof NotFoundError) return t("refusal.notFound");
  const status = (error as { status?: unknown } | null)?.status;
  if (typeof status === "number" && status >= 500) return t("refusal.unavailable");
  const message = error instanceof Error ? error.message : "";
  return message || t("refusal.unknown");
};

/** A line's refusal code in words (the problem's `lines`), or the server's when this version has none. */
export const lineRefusalWords = (code: string, detail: string, t: Translate): string =>
  hasWords(`refusal.${code}`) ? t(`refusal.${code}`) : detail;
