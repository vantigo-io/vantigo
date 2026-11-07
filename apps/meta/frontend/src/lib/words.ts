import { type ApiError, ApiValidationError, NotFoundError } from "../api/request";
import { metaCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
export type Translate = (key: string, values?: Record<string, unknown>) => string;

const catalog = metaCatalog.en as Record<string, string>;
const known = (key: string) => key in catalog;

/** The `code` a `MetaProblem` carries, when it carries one. */
export const codeOf = (error: unknown): string | undefined => {
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** The Pages a page_choice_required refusal lists; none for any other error. */
export const choicesOf = (error: unknown): { id: string; name: string }[] => {
  if (codeOf(error) !== "page_choice_required") return [];
  const choices = (error as { problem?: { choices?: unknown } }).problem?.choices;
  if (!Array.isArray(choices)) return [];
  return choices.filter(
    (c): c is { id: string; name: string } => typeof c?.id === "string" && typeof c?.name === "string",
  );
};

/**
 * A refusal in the reader's words: a coded one by its code
 * (`refusal.<code>`), never the server's English; a validation error by its
 * first field's sentence; anything else by its own message.
 */
export const refusalText = (error: unknown, t: Translate): string => {
  const code = codeOf(error);
  if (code && known(`refusal.${code}`)) return t(`refusal.${code}`);
  if ((error as ApiError | undefined)?.status === 429) return t("refusal.too_many_requests");
  if (error instanceof NotFoundError) return t("refusal.page_not_found");
  if (error instanceof ApiValidationError) return Object.values(error.fieldErrors)[0] ?? error.message;
  return error instanceof Error ? error.message : String(error);
};

/** The status line's last Graph error code in words when the catalogue has it (`graphError.<code>`); otherwise as it came. */
export const graphErrorText = (code: string, t: Translate): string =>
  known(`graphError.${code}`) ? t(`graphError.${code}`) : code;
