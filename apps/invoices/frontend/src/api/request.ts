import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl, sessionNotFoundMeansExpired: false });

export const { request } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiConflictError, ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/**
 * The host's session handling, handed to the shared client: the host installs
 * both when it mounts the package, and the test setup clears them after each
 * test.
 */
let unauthorized: (() => void | Promise<void>) | undefined;
let clearAuthState: (() => void | Promise<void>) | undefined;

export const setUnauthorizedHandler = (handler: (() => void | Promise<void>) | undefined) => {
  unauthorized = handler;
  client.setUnauthorizedHandler(handler);
};

export const setAuthStateClearer = (clearer: (() => void | Promise<void>) | undefined) => {
  clearAuthState = clearer;
  client.setAuthStateClearer(clearer);
};

/**
 * What the shared client does on a 401, for the one request that cannot go
 * through it — a PDF, which is a blob, not JSON: the session is cleared and the
 * host told, so an expired session signs the person in again rather than
 * reading as a PDF that could not be opened.
 */
export const sessionExpired = async (): Promise<void> => {
  try {
    await clearAuthState?.();
  } finally {
    await unauthorized?.();
  }
};

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
export const INVOICES_QUERY_KEY = "invoices";
