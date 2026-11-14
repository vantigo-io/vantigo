import "../i18n";
import { useInvoiceFormat } from "./format";

/** A Norwegian account number as the bank writes it: 8601.11.17947. Anything else as it is. */
export const accountNumber = (account: string): string =>
  /^\d{11}$/.test(account) ? `${account.slice(0, 4)}.${account.slice(4, 6)}.${account.slice(6)}` : account;

/**
 * Who did something, in words: the wire carries a user's id alone, and this
 * module reads no user directory, so the reader is "you" and anyone else
 * "another user".
 */
export const useWho = (currentUserId: string | undefined) => {
  const { t } = useInvoiceFormat();
  return (userId: string | undefined) => (userId && userId === currentUserId ? t("bank.you") : t("bank.anotherUser"));
};

/**
 * useWho for what the bank feed may have done by itself (invoices bank feed
 * design F6): a feed batch no person uploaded, a line it matched or queued,
 * a payment it registered and the waiver it wrote carry no user, and say
 * "the bank feed" where a person would be named.
 */
export const useWhoOrFeed = (currentUserId: string | undefined) => {
  const { t } = useInvoiceFormat();
  const who = useWho(currentUserId);
  return (userId: string | undefined) => (userId ? who(userId) : t("bank.byFeed"));
};
