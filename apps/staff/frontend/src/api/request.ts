import { createApiClient } from "@vantigo/frontend-api-client";
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
