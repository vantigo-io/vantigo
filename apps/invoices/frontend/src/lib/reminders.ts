import type { ChargeKind } from "../api/charges";
import type { InvoiceDocument } from "../api/invoices";
import type { Reminder, ReminderStatus } from "../api/reminders";
import { invoicesCatalog } from "../i18n";
import { useWho } from "./bank";
import { useInvoiceFormat } from "./format";

/** The colour a letter's status badge has. */
export const statusColour: Record<ReminderStatus, string> = {
  queued: "blue",
  awaiting_print: "blue",
  printed: "cyan",
  sent: "green",
  failed: "red",
  withdrawn: "gray",
};

/** A key in words when this catalog has it, the code itself otherwise — a newer server's code never throws. */
export const useWords = () => {
  const { t } = useInvoiceFormat();
  return (key: string, code: string, values?: Record<string, unknown>) =>
    key in invoicesCatalog.en ? t(key, values) : code;
};

/** A letter's level as a person reads it: a reminder, one announcing the hand-off, or a debt collection notice. */
export const useLetterLevel = () => {
  const { t } = useInvoiceFormat();
  return (letter: { level: Reminder["level"]; announcesCollection: boolean }) =>
    letter.level === "collection_notice"
      ? t("reminder.level.collection_notice")
      : letter.announcesCollection
        ? t("reminder.level.announcing")
        : t("reminder.level.reminder");
};

/** "Letter 2", the name every action on a letter is labelled by. */
export const letterName = (t: (key: string, values?: Record<string, unknown>) => string, sequence: number) =>
  t("reminder.letterN", { n: sequence });

/** One charge a waiver may release: a sent letter's fee or compensation, or the interest the latest one claimed. */
export interface WaivableCharge {
  reminderId: number;
  sequence: number;
  kind: ChargeKind;
  /** The letter's whole fee or compensation; for interest, what the latest letter claimed, before waivers and payments. */
  amount: number;
}

/**
 * The charges a waiver may name (D9): each sent letter's fee and compensation
 * not waived already, and — as an amount the server works out — the interest
 * the latest sent letter claimed, which every interest waiver names.
 */
export const waivableCharges = (invoice: InvoiceDocument): WaivableCharge[] => {
  const sent = (invoice.reminders ?? []).filter((r) => r.status === "sent");
  const waived = new Set((invoice.waivers ?? []).map((w) => `${w.reminderId}:${w.kind}`));
  const charges: WaivableCharge[] = [];
  for (const r of sent) {
    if ((r.fee ?? 0) > 0 && !waived.has(`${r.id}:fee`)) {
      charges.push({ reminderId: r.id, sequence: r.sequence, kind: "fee", amount: r.fee ?? 0 });
    }
    if ((r.compensation ?? 0) > 0 && !waived.has(`${r.id}:compensation`)) {
      charges.push({ reminderId: r.id, sequence: r.sequence, kind: "compensation", amount: r.compensation ?? 0 });
    }
  }
  const latest = sent.reduce<(typeof sent)[number] | undefined>(
    (found, r) => (!found || r.sequence > found.sequence ? r : found),
    undefined,
  );
  if (latest && (latest.interest ?? 0) > 0) {
    charges.push({ reminderId: latest.id, sequence: latest.sequence, kind: "interest", amount: latest.interest ?? 0 });
  }
  return charges;
};

/**
 * What a letter's status needs said beside it: why a queued one waits, why a
 * failed one failed, why and by whom one was withdrawn — a code of the
 * module's in words, a person's own words quoted — and a printed one's batch.
 */
export const useLetterStatusLine = (currentUserId: string | undefined) => {
  const { t } = useInvoiceFormat();
  const words = useWords();
  const who = useWho(currentUserId);
  return (letter: Reminder): string | undefined => {
    if (letter.status === "queued" && letter.heldReason)
      return words(`reminder.held.${letter.heldReason}`, letter.heldReason);
    if (letter.status === "failed")
      return t("reminder.failedAfter", { attempts: letter.attempts, error: letter.lastError ?? "" });
    if (letter.status === "withdrawn") {
      const reason = letter.withdrawnBy
        ? (letter.withdrawalReason ?? "")
        : words(`reminder.withdrawnReason.${letter.withdrawalReason}`, letter.withdrawalReason ?? "");
      return letter.withdrawnBy
        ? t("reminder.withdrawnBy", { who: who(letter.withdrawnBy), reason })
        : t("reminder.withdrawnByVantigo", { reason });
    }
    if (letter.status === "printed" && letter.printBatchId !== undefined) {
      return t("reminder.inBatch", { batch: letter.printBatchId });
    }
    return undefined;
  };
};
