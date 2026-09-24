import { ApiConflictError } from "../api/request";

/**
 * The code every customer-scoped write answers once its customer has been
 * merged into another (customers merge design D4): the page was opened before
 * the merge — a stale tab — and the customer it edits is read-only now.
 */
export const CUSTOMER_MERGED_CODE = "customer_merged";

export const isCustomerMerged = (error: unknown): boolean =>
  error instanceof ApiConflictError && error.code === CUSTOMER_MERGED_CODE;

/**
 * What a failed customer-scoped write says. The merged-away refusal gets its
 * own sentence, in the reader's language, rather than the server's English
 * detail; everything else keeps the server's own words, as before. Shared by
 * every write on the customer page (and the contact page's writes that land on
 * a customer), so a stale tab reads the same thing whichever card it used.
 */
export const customerWriteErrorMessage = (error: Error, t: (key: string) => string): string =>
  isCustomerMerged(error) ? t("customerMergedMessage") : error.message;
