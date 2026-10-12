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
 * invalidation each write does reaches all of them — `/meta`'s structure
 * included, which the People filters and the New person form read.
 */
export const STAFF_QUERY_KEY = "staff";

/** One page of a list, as every paged endpoint of this module answers it. */
export interface Paginated<T> {
  data: T[];
  pagination: {
    page: number;
    pageSize: number;
    totalCount: number;
    totalPages: number;
    hasNextPage: boolean;
    hasPreviousPage: boolean;
  };
}

/**
 * A refused answer as the shared client throws it, so the page says it in the
 * same words — but with the problem's `code` kept on a validation refusal
 * too, which the shared client drops: an import's file refusals
 * (`import_sealed_columns` …) and the payroll month's are field errors whose
 * code is what the page words.
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
  return Object.assign(new Error(message), { status: response.status, code: problem?.code });
};
