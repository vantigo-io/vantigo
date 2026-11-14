import {
  ApiConflictError,
  ApiValidationError,
  createApiClient,
  NotFoundError,
  type RequestOptions,
  readJson,
} from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

/** The shared client's request: every read in this package. */
export const request = <T>(url: string, init: RequestOptions = {}): Promise<T> => client.request<T>(url, init);

export type { ApiError } from "@vantigo/frontend-api-client";
export { ApiConflictError, ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";
export type { RequestOptions };

/**
 * The host's session handling, kept here as well as handed to the client.
 * The writes in this package go past `request` (`send`), so their refusals
 * keep the code and the field errors; an expired session on one must still
 * sign the person out.
 */
let onUnauthorized: (() => void | Promise<void>) | undefined;
let clearAuthState: (() => void | Promise<void>) | undefined;

export const setUnauthorizedHandler = (handler: (() => void | Promise<void>) | undefined) => {
  onUnauthorized = handler;
  client.setUnauthorizedHandler(handler);
};

export const setAuthStateClearer = (clearer: (() => void | Promise<void>) | undefined) => {
  clearAuthState = clearer;
  client.setAuthStateClearer(clearer);
};

/** What the shared client does on a 401: clear the session, then tell the host. */
export const handleUnauthorized = async (): Promise<void> => {
  try {
    await clearAuthState?.();
  } finally {
    await onUnauthorized?.();
  }
};

/** A JSON write's init. */
export const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so the blanket
 * invalidation each write does reaches all of them: a voucher posted changes
 * the list, the reports and the overview at once.
 */
export const ACCOUNTING_QUERY_KEY = "accounting";

/** A query string of the parameters that are set; empty when none is. */
export const queryString = (params: Record<string, string | number | boolean | undefined | null>): string => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== null && value !== "") search.set(key, String(value));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/**
 * Every refusal code the module answers (accounting design §2, §13), in the
 * contract's order; each has its words in the catalog (`refusal.<code>`).
 */
export const refusalCodes = [
  "books_not_set_up",
  "books_already_set_up",
  "revision_conflict",
  "account_number_taken",
  "account_has_postings",
  "account_has_role",
  "account_inactive",
  "account_unknown",
  "tax_code_unknown",
  "tax_code_no_rate",
  "rate_period_invalid",
  "party_required",
  "party_not_allowed",
  "open_item_unknown",
  "voucher_unbalanced",
  "voucher_empty",
  "amount_invalid",
  "date_before_books_start",
  "period_locked",
  "voucher_owned_by_source",
  "voucher_already_reversed",
  "opening_exists",
  "opening_locked",
  "lock_not_month_end",
  "lock_reason_required",
  "lock_before_books_start",
  "settlement_source_document",
  "settlement_exceeds_balance",
  "settlement_settled",
  "bank_account_invalid",
  "attachment_too_large",
  "storage_not_configured",
  "report_account_required",
  "period_invalid",
  "open_item_source_document",
  "report_too_large",
  "lock_in_future",
  "books_start_too_early",
  "account_kind_mismatch",
  "reverse_charge_gross",
  "posting_rule_name_taken",
  "posting_rule_match_taken",
  "posting_rule_default",
  "unposted_already_posted",
  "supplier_organisation_number_taken",
  "supplier_bank_account_invalid",
  "supplier_has_postings",
  "supplier_inactive",
  "duplicate_supplier_invoice",
  "supplier_invoice_not_draft",
  "supplier_invoice_not_posted",
  "supplier_invoice_file_required",
  "supplier_invoice_total_mismatch",
  "tax_code_not_purchase",
  "exchange_rate_required",
  "supplier_invoice_settled",
  "payment_exceeds_balance",
  "payment_removed",
  "settlement_supplier_invoice",
  "unposted_wrong_reason",
  "brreg_unavailable",
  "supplier_invoice_has_payments",
  "tax_code_not_for_supplier_invoice",
  "exchange_rate_mismatch",
  "credit_note_invoice_not_posted",
  "credit_note_currency_mismatch",
  "credit_note_exceeds_balance",
  "supplier_invoice_item_in_use",
  "supplier_retained",
  "supplier_anonymised",
  "expense_already_invoiced",
  "vat_not_registered",
  "vat_term_closed",
  "vat_term_not_ended",
  "vat_term_out_of_order",
  "vat_term_attention",
  "vat_term_lock_after_end",
  "vat_term_unreconciled",
  "vat_term_not_closed",
  "vat_term_filed",
  "vat_term_not_latest",
  "vat_term_not_filed",
  "lock_behind_filed_term",
  "lock_behind_closed_term",
  "vat_term_stale",
  "opening_vat_account",
  "tax_code_in_use",
  "vat_term_inconsistent",
  "vat_term_partial",
  "vat_term_rate_missing",
  "saft_seller_missing",
  "saft_file_invalid",
  "saft_version_unsupported",
  "saft_file_too_large",
  "saft_import_ledger_has_vouchers",
  "saft_import_applied",
  "saft_import_unresolved",
  "saft_import_unmatched_customers",
  "saft_import_parties_mismatch",
  "saft_import_books_start_mismatch",
  "saft_import_link_unknown",
  "saft_import_other_company",
  "saft_export_file_removed",
  "bank_account_number_invalid",
  "bank_account_currency",
  "bank_account_number_taken",
  "bank_account_ledger_taken",
  "bank_account_has_statements",
  "bank_feed_off",
  "bank_feed_not_connected",
  "bank_feed_refused",
  "bank_feed_account_not_offered",
  "bank_feed_unreachable",
  "bank_statement_invalid",
  "bank_statement_too_large",
  "bank_statement_account_unknown",
  "bank_statement_account_required",
  "bank_csv_mapping_required",
  "bank_csv_mapping_invalid",
  "bank_line_matched",
  "bank_line_not_matched",
  "bank_suggestion_stale",
  "bank_posting_matched",
  "bank_line_customer_payment",
  "bank_rule_in_use",
  "bank_unreconciled",
  "bank_account_in_use",
  "bank_feed_start_too_early",
  "bank_rule_customer_payment",
  "bank_amount_currency_required",
  "bank_fee_too_large",
  "bank_open_item_required",
  "bank_line_possible_duplicate",
  "bank_line_not_duplicate",
] as const;
export type RefusalCode = (typeof refusalCodes)[number];

/**
 * A refused answer as an error the page can say in its own words: the
 * problem's code kept on a validation refusal too, which the shared client
 * drops, and its field errors by camelCase path.
 */
export const refusalOf = async (response: Response): Promise<Error> => {
  const problem = await readJson<{
    title?: string;
    detail?: string;
    code?: string;
    errors?: Record<string, string[]>;
  }>(response).catch(() => null);
  const message = problem?.detail ?? problem?.title ?? "";
  const extra = { status: response.status, code: problem?.code ?? undefined };
  if (response.status === 404) return Object.assign(new NotFoundError(message), { ...extra, status: 404 as const });
  if (problem?.errors) return Object.assign(new ApiValidationError(message, problem.errors, response.status), extra);
  if (response.status === 409 && problem?.title)
    return new ApiConflictError(message, {
      title: problem.title,
      detail: problem.detail,
      code: problem.code,
      problem: problem as Record<string, unknown>,
    });
  return Object.assign(new Error(message), extra);
};

/** A write sent past the shared client, so its refusal keeps its code (`refusalOf`). */
export const send = async <T>(url: string, init: RequestInit): Promise<T> => {
  const response = await fetch(appUrl(url), { ...init, credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (response.status === 204 ? undefined : await response.json()) as T;
  throw await refusalOf(response);
};

/** The rule a refusal names, whichever shape it was thrown in. */
export const refusalCode = (error: unknown): string | undefined => {
  if (error instanceof ApiConflictError) return error.code;
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** A refusal's field errors by camelCase path (`lines[3].amount`), first message each; empty when none. */
export const refusedFields = (error: unknown): Readonly<Record<string, string>> =>
  error instanceof ApiValidationError ? error.fieldErrors : {};
