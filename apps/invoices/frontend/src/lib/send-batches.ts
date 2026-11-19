import { invoicesCatalog } from "../i18n";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

export const jobStateColour: Record<string, string> = {
  queued: "gray",
  sending: "blue",
  sent: "green",
  handed_over: "green",
  awaiting_print: "yellow",
  printed: "green",
  skipped: "gray",
  failed: "red",
  uncertain: "orange",
};

/** A send job's state in words, or the state itself when this version has no words for it. */
export const jobStateWords = (state: string, t: Translate) =>
  `sendAll.state.${state}` in invoicesCatalog.en ? t(`sendAll.state.${state}`) : state;

/** The channel a job went by, in words. */
export const jobChannelWords = (channel: string, t: Translate) =>
  `sendAll.channel.${channel}` in invoicesCatalog.en ? t(`sendAll.channel.${channel}`) : channel;

/**
 * Why a job was skipped, failed or is uncertain, in words: send all's own
 * wording (`sendAll.code.<code>`) first, the module's `refusal.<code>` next —
 * a send's refusal is the send's own —, and the code itself otherwise.
 */
export const jobCodeWords = (code: string, t: Translate) => {
  if (`sendAll.code.${code}` in invoicesCatalog.en) return t(`sendAll.code.${code}`);
  if (`refusal.${code}` in invoicesCatalog.en) return t(`refusal.${code}`);
  return t("billingRun.codeUnknown", { code });
};
