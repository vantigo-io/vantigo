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
 * keep the code and the lines; an expired session on one must still sign the
 * person out. Holding the two callbacks lets `handleUnauthorized` do exactly
 * what the client does on a 401.
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
 * Every write in this package sends JSON and carries the session cookie. A
 * save names the version (an order) or the revision (a quote) the form was
 * opened at in its body, so somebody else's save in between is a 409
 * `version_conflict` rather than being silently overwritten (orders design O26).
 */
export const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so the blanket
 * invalidation each write does reaches all of them: an order's change moves
 * its list, its page and the customer's summary at once.
 */
export const ORDERS_QUERY_KEY = "orders";

/** A query string of the parameters that are set; empty when none is. */
export const queryString = (params: Record<string, string | number | boolean | undefined>): string => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== "") search.set(key, String(value));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/** One failing line of a refused save: its index in the request, its code and its words. */
export interface RefusedLine {
  index: number;
  code: string;
  detail: string;
}

/**
 * A refused answer as the shared client throws it, so the page says it in the
 * same words — but with the problem's `code` kept on a validation refusal
 * too, which the shared client drops, and a document's failing `lines`, which
 * the line editor marks row by row. An answer without a problem's words has
 * an empty message and its `status`, which `refusalMessage` says in the
 * viewer's language.
 */
export const refusalOf = async (response: Response): Promise<Error> => {
  const problem = await readJson<{
    title?: string;
    detail?: string;
    code?: string;
    errors?: Record<string, string[]>;
    lines?: RefusedLine[];
  }>(response).catch(() => null);
  const message = problem?.detail ?? problem?.title ?? "";
  const extra = { code: problem?.code ?? undefined, lines: problem?.lines ?? undefined };
  if (response.status === 404) return new NotFoundError(message);
  if (problem?.errors) return Object.assign(new ApiValidationError(message, problem.errors, response.status), extra);
  if (response.status === 409 && problem?.title)
    return Object.assign(
      new ApiConflictError(message, { title: problem.title, detail: problem.detail, code: problem.code, problem }),
      { lines: extra.lines },
    );
  return Object.assign(new Error(message), { status: response.status, ...extra });
};

/** A write sent past the shared client, so its refusal keeps the code and the lines (`refusalOf`). */
export const send = async <T>(url: string, init: RequestInit): Promise<T> => {
  const response = await fetch(appUrl(url), { ...init, credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (response.status === 204 ? undefined : await response.json()) as T;
  throw await refusalOf(response);
};

/** The rule a refusal names (`line_unpriced`, `order_locked` …), whichever shape the client threw it in. */
export const refusalCode = (error: unknown): string | undefined => {
  if (error instanceof ApiConflictError) return error.code;
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** A refused save's failing lines, empty when the refusal was about the request as a whole. */
export const refusedLines = (error: unknown): readonly RefusedLine[] => {
  const lines = (error as { lines?: unknown } | null)?.lines;
  return Array.isArray(lines) ? (lines as RefusedLine[]) : [];
};

/** Whether a save was refused because the document moved on since it was read. */
export const isVersionConflict = (error: unknown): boolean => refusalCode(error) === "version_conflict";
