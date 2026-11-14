import { useI18n } from "@vantigo/frontend-shell";
import { ApiConflictError, ApiValidationError, refusalCode, refusalCodes } from "../api/request";
import "../i18n";

type TFunction = ReturnType<typeof useI18n>["t"];

/** The package's words and its ways of showing amounts and dates, in the viewer's language. */
export const useAccountingFormat = () => {
  const { t, locale, formatters } = useI18n("accounting");
  return {
    t,
    locale,
    /** An exact amount in NOK, as the other apps write money: `kr 1 234,50`. */
    money: (text: string | undefined | null) =>
      text === undefined || text === null || text === "" ? "" : formatters.formatCurrency(Number(text), "NOK"),
    /** An exact amount without the currency: `1 234,50`. */
    amount: (text: string | undefined | null) =>
      text === undefined || text === null || text === ""
        ? ""
        : formatters.formatNumber(Number(text), { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
    /** A calendar date (`2026-11-12`), as the day it names wherever the viewer is. */
    day: (date: string | undefined | null) =>
      date ? formatters.formatDate(`${date}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" }) : "",
    /** An instant, day and time. */
    stamp: (instant: string) => formatters.formatDate(instant, { dateStyle: "medium", timeStyle: "short" }),
  };
};

/**
 * The words for a refusal: the module's own for a code it answers
 * (`refusal.<code>`), else what the server said — a field error's first
 * message, or the problem's detail. A failure with no HTTP status never
 * reached the server and says so.
 */
export const refusalMessage = (t: TFunction, error: Error): string => {
  const status = (error as { status?: unknown }).status;
  if (typeof status !== "number") return t("couldNotReach");
  const code = refusalCode(error);
  if (code && (refusalCodes as readonly string[]).includes(code)) {
    const detail =
      error instanceof ApiValidationError
        ? Object.values(error.fieldErrors)[0]
        : error instanceof ApiConflictError
          ? error.detail
          : undefined;
    return detail ? `${t(`refusal.${code}`)} ${detail}` : t(`refusal.${code}`);
  }
  if (error instanceof ApiValidationError) {
    const first = Object.values(error.fieldErrors)[0];
    if (first) return first;
  }
  return error.message || t("requestFailed", { status });
};
