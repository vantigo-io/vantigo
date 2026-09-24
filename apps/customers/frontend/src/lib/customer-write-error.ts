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
 * The code every customer-scoped write answers once its customer has been
 * anonymised (customers GDPR design D4): what is left is kept for bookkeeping
 * and takes no more changes.
 */
export const CUSTOMER_ANONYMISED_CODE = "customer_anonymised";

export const isCustomerAnonymised = (error: unknown): boolean =>
  error instanceof ApiConflictError && error.code === CUSTOMER_ANONYMISED_CODE;

/** Either refusal a customer that takes no more changes answers: merged away, or anonymised. */
export const isCustomerReadOnly = (error: unknown): boolean => isCustomerMerged(error) || isCustomerAnonymised(error);

/**
 * What a failed customer-scoped write says. The merged-away and anonymised
 * refusals each get their own sentence, in the reader's language, rather than
 * the server's English detail; everything else keeps the server's own words, as before. Shared by
 * every write on the customer page (and the contact page's writes that land on
 * a customer), so a stale tab reads the same thing whichever card it used.
 */
export const customerWriteErrorMessage = (error: Error, t: (key: string) => string): string =>
  isCustomerMerged(error)
    ? t("customerMergedMessage")
    : isCustomerAnonymised(error)
      ? t("customerAnonymisedMessage")
      : error.message;
