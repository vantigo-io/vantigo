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
