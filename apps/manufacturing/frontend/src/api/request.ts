import {
  ApiConflictError,
  ApiValidationError,
  createApiClient,
  NotFoundError,
  readJson,
} from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

export const { request } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiConflictError, ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/**
 * The host's session handling, kept here as well as handed to the client.
 * The writes in this package go past `request` (`send`), so their refusals
 * keep the code, the lines and a cycle's path; an expired session on one must
 * still sign the person out. Holding the two callbacks lets
 * `handleUnauthorized` do exactly what the client does on a 401.
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

/**
 * Every write in this package sends JSON and carries the session cookie. The
 * revision a replace is guarded by travels in the body (`revision`), as the
 * contract asks, so a save in between is a 409 `revision_conflict`.
 */
export const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so the blanket
 * invalidation each write does reaches all of them: a BoM saved changes the
 * list, the stats and every overview at once.
 */
export const MANUFACTURING_QUERY_KEY = "manufacturing";

/** A query string of the parameters that are set; empty when none is. */
export const queryString = (params: Record<string, string | number | boolean | undefined>): string => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== "") search.set(key, String(value));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/**
 * Every refusal code the module answers (manufacturing design §3, §15), in
 * the contract's order; each has its words in the catalog (`refusal.<code>`).
 */
export const refusalCodes = [
  "revision_conflict",
  "work_center_code_taken",
  "work_center_in_use",
  "work_center_has_open_work",
  "closures_too_many",
  "site_unknown",
  "site_archived",
  "costs_view_required",
  "variant_not_goods",
  "variant_not_found",
  "bom_code_taken",
  "too_many_lines",
  "too_many_operations",
  "operation_key_unknown",
  "work_center_unknown",
  "work_center_other_site",
  "bom_cycle",
  "bom_too_deep",
  "bom_too_large",
  "bom_not_active",
  "quantity_invalid",
  "bom_required",
  "location_unknown",
  "required_below_used",
  "order_not_draft",
  "order_not_open",
  "order_has_production",
  "consumption_differs",
  "quantity_short",
  "client_request_reused",
  "shop_floor_required",
  "work_center_blocked",
  "work_order_pending",
  "work_order_closed",
  "log_conflict",
  "log_locked",
  "order_closed",
  "log_overlaps",
  "no_capacity",
  "period_too_long",
  "insufficient_stock",
  "insufficient_available",
  "variant_not_stocked",
  "document_reused",
  "dependency_unavailable",
  "lot_pattern_invalid",
  "picks_mismatch",
  "serial_quantity",
  "lot_not_allowed",
  "lot_required",
  "lot_unknown",
  "lot_expired",
  "lot_blocked",
  "lot_dates_differ",
  "lot_exists",
  "check_limits_invalid",
  "photo_required",
  "checks_incomplete",
  "check_not_open",
  "deviation_closed",
  "lot_not_of_order",
  "lots_view_required",
  "attachment_too_large",
  "attachment_type",
  "storage_not_configured",
  "suggestion_not_open",
  "operation_order",
  "operation_unjoined",
  "operation_unknown",
  "work_center_not_in_group",
  "work_order_started",
  "work_order_member_fixed",
  "work_order_is_output",
  "quantity_over_order",
  "work_center_group_empty",
  "output_is_product",
  "output_is_component",
  "cost_share_over",
  "unbuild_too_much",
  "split_sum",
  "merge_mismatch",
  "order_has_work",
] as const;
export type RefusalCode = (typeof refusalCodes)[number];

/** One failing line of a refused BoM: its index in the request, its code and its words. */
export interface RefusedLine {
  index: number;
  code: string;
  detail: string;
}

/** A consumed quantity more than 10 % off its proportional figure (`consumption_differs`, M22). */
export interface Deviation {
  componentId: number;
  sku: string;
  expected: string;
  consumed: string;
}

/** A check with no result that stops a production or a finish (`checks_incomplete`, manufacturing phase 2 M98). */
export interface MissingCheck {
  checkId: number;
  title: string;
  frequency: string;
  workOrderId: number;
  workOrderName: string;
  workOrderSequence: number;
}

/** One variant of a refused cycle's path (`bom_cycle`). */
export interface CycleStep {
  variantId: number;
  sku: string;
}

/**
 * A refused answer as the shared client throws it, so the page says it in the
 * same words — but with the problem's `code` kept on a validation refusal
 * too, which the shared client drops, a BoM's failing `lines`, which the
 * editor marks row by row, and a cycle's `path`. An answer without a
 * problem's words has an empty message and its `status`, which
 * `refusalMessage` says in the viewer's language.
 */
export const refusalOf = async (response: Response): Promise<Error> => {
  const problem = await readJson<{
    title?: string;
    detail?: string;
    code?: string;
    errors?: Record<string, string[]>;
    lines?: RefusedLine[];
    path?: CycleStep[];
    deviations?: Deviation[];
    checks?: MissingCheck[];
  }>(response).catch(() => null);
  const message = problem?.detail ?? problem?.title ?? "";
  const extra = {
    status: response.status,
    code: problem?.code ?? undefined,
    lines: problem?.lines ?? undefined,
    path: problem?.path ?? undefined,
    deviations: problem?.deviations ?? undefined,
    checks: problem?.checks ?? undefined,
  };
  if (response.status === 404) return Object.assign(new NotFoundError(message), { ...extra, status: 404 as const });
  if (problem?.errors) return Object.assign(new ApiValidationError(message, problem.errors, response.status), extra);
  if (response.status === 409 && problem?.title)
    return Object.assign(
      new ApiConflictError(message, { title: problem.title, detail: problem.detail, code: problem.code, problem }),
      { lines: extra.lines, path: extra.path, deviations: extra.deviations, checks: extra.checks },
    );
  return Object.assign(new Error(message), extra);
};

/** A write sent past the shared client, so its refusal keeps the code, the lines and the path (`refusalOf`). */
export const send = async <T>(url: string, init: RequestInit): Promise<T> => {
  const response = await fetch(appUrl(url), { ...init, credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (response.status === 204 ? undefined : await response.json()) as T;
  throw await refusalOf(response);
};

/** The rule a refusal names (`bom_cycle`, `work_center_in_use` …), whichever shape the client threw it in. */
export const refusalCode = (error: unknown): string | undefined => {
  if (error instanceof ApiConflictError) return error.code;
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** A refused BoM's failing lines, empty when the refusal was about the request as a whole. */
export const refusedLines = (error: unknown): readonly RefusedLine[] => {
  const lines = (error as { lines?: unknown } | null)?.lines;
  return Array.isArray(lines) ? (lines as RefusedLine[]) : [];
};

/** A refused cycle's variants, from the BoM's own; empty for any other refusal. */
export const refusedPath = (error: unknown): readonly CycleStep[] => {
  const path = (error as { path?: unknown } | null)?.path;
  return Array.isArray(path) ? (path as CycleStep[]) : [];
};

/** A refused production's deviating lines (`consumption_differs`); empty for any other refusal. */
export const refusedDeviations = (error: unknown): readonly Deviation[] => {
  const deviations = (error as { deviations?: unknown } | null)?.deviations;
  return Array.isArray(deviations) ? (deviations as Deviation[]) : [];
};

/** A refusal's field errors by camelCase path (`lines[3].quantity`), first message each; empty when none. */
export const refusedFields = (error: unknown): Readonly<Record<string, string>> =>
  error instanceof ApiValidationError ? error.fieldErrors : {};

/** Whether a replace was refused because the row moved on since it was read. */
export const isRevisionConflict = (error: unknown): boolean => refusalCode(error) === "revision_conflict";

/** A refused production's or finish's missing checks (`checks_incomplete`); empty for any other refusal. */
export const refusedChecks = (error: unknown): readonly MissingCheck[] => {
  const checks = (error as { checks?: unknown } | null)?.checks;
  return Array.isArray(checks) ? (checks as MissingCheck[]) : [];
};
