import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

export const { request, setAuthStateClearer, setUnauthorizedHandler } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/** Every write in this package sends JSON and carries the session cookie. */
export const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so the blanket
 * invalidation each write does reaches all of them.
 */
export const EXPENSES_QUERY_KEY = "expenses";
