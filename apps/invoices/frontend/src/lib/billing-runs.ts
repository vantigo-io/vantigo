import { invoicesCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

/** How often a page showing a run the worker is still carrying reads it again. */
export const BILLING_RUN_POLL_MS = 5000;

export const runStatusColour: Record<string, string> = {
  queued: "gray",
  running: "blue",
  done: "green",
  cancelled: "dark",
};

export const itemStatusColour: Record<string, string> = {
  queued: "gray",
  drafted: "blue",
  issued: "green",
  skipped: "yellow",
  failed: "red",
  cancelled: "dark",
};

/** A run's status in words, or the status itself when this version has no words for it. */
export const runStatusWords = (status: string, t: Translate) =>
  `billingRun.status.${status}` in invoicesCatalog.en ? t(`billingRun.status.${status}`) : status;

/** An item's status in words, or the status itself. */
export const itemStatusWords = (status: string, t: Translate) =>
  `billingRun.itemStatus.${status}` in invoicesCatalog.en ? t(`billingRun.itemStatus.${status}`) : status;

/**
 * Why an item was skipped or failed, in words: the run's own wording
 * (`billingRun.code.<code>`) first, the module's `refusal.<code>` next — an
 * issue refusal is the issue's own —, and the code itself otherwise.
 */
export const itemCodeWords = (code: string, t: Translate) => {
  if (`billingRun.code.${code}` in invoicesCatalog.en) return t(`billingRun.code.${code}`);
  if (`refusal.${code}` in invoicesCatalog.en) return t(`refusal.${code}`);
  return t("billingRun.codeUnknown", { code });
};
