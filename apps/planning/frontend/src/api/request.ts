import {
  ApiConflictError,
  ApiValidationError,
  createApiClient,
  NotFoundError,
  readJson,
} from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

export const { request, setAuthStateClearer, setUnauthorizedHandler } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiConflictError, ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/**
 * Every write in this package sends JSON and carries the session cookie. A
 * replace is guarded by the revision the form was opened at (`If-Match`), so
 * somebody else's save in between is a 409 `revision_conflict` rather than
 * being silently overwritten.
 */
export const json = (method: string, body: unknown, revision?: number): RequestInit => ({
  method,
  headers: {
    "Content-Type": "application/json",
    ...(revision === undefined ? {} : { "If-Match": String(revision) }),
  },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so the blanket
 * invalidation each write does reaches all of them — the board, the plans,
 * the timelines and the unbooked list all read the same rows.
 */
export const PLANNING_QUERY_KEY = "planning";

/** A query string of the parameters that are set; empty when none is. */
export const queryString = (params: Record<string, string | number | boolean | undefined>): string => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== "") search.set(key, String(value));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/**
 * A refused answer as the shared client throws it, so the page says it in the
 * same words — but with the problem's `code` kept on a validation refusal
 * too, which the shared client drops: `period_too_long` is a field error on
 * `toDate` whose code is what the sheet words.
 */
export const refusalOf = async (response: Response): Promise<Error> => {
  const problem = await readJson<{ title?: string; detail?: string; code?: string; errors?: Record<string, string[]> }>(
    response,
  ).catch(() => null);
  const message = problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`;
  if (response.status === 404) return new NotFoundError(message);
  if (problem?.errors)
    return Object.assign(new ApiValidationError(message, problem.errors, response.status), { code: problem.code });
  if (response.status === 409 && problem?.title)
    return new ApiConflictError(message, { title: problem.title, detail: problem.detail, code: problem.code, problem });
  return Object.assign(new Error(message), { status: response.status, code: problem?.code ?? undefined });
};

/**
 * A write sent past the shared client, so its refusal keeps the code
 * (`refusalOf`). Used by the allocation and request sheets' create and
 * replace, whose field errors carry a rule's code, by a request's fill and
 * withdrawal, by the terms and by the wishes.
 */
export const send = async <T>(url: string, init: RequestInit): Promise<T> => {
  const response = await fetch(appUrl(url), { ...init, credentials: "include" });
  if (response.ok) return (response.status === 204 ? undefined : await response.json()) as T;
  throw await refusalOf(response);
};

/** The rule a refusal names (`overbooked`, `project_closed` …), whichever shape the client threw it in. */
export const refusalCode = (error: unknown): string | undefined => {
  if (error instanceof ApiConflictError) return error.code;
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/**
 * The refusals a write, a confirm, a delete, a fill, a withdrawal or a wish
 * answers as a rule (409) rather than a field (§4.7), each worded by its code
 * (`refusal.<code>`).
 */
export const ruleRefusals: readonly string[] = [
  "overbooked",
  "outside_employment",
  "project_closed",
  "request_not_open",
  "dependency_unavailable",
  "non_compete",
  "wish_exists",
  "wish_limit",
  "wish_no_booking",
];

/** Whether a replace was refused because the row moved on since it was read. */
export const isRevisionConflict = (error: unknown): boolean => refusalCode(error) === "revision_conflict";
