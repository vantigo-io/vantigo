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
