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
 * keep the code; an expired session on one must still sign the person out.
 * Holding the two callbacks lets `handleUnauthorized` do exactly what the
 * client does on a 401.
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
 * content save names the version the editor last read (`expectedVersion`), so
 * somebody else's save in between is a 409 `version_conflict` rather than
 * being silently overwritten (knowledge design K9).
 */
export const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/**
 * The one prefix every query key in this package starts with, so a blanket
 * invalidation — a live `tree.changed`, say (K13) — reaches all of them.
 */
export const KNOWLEDGE_QUERY_KEY = "knowledge";

/**
 * A refused answer as the shared client throws it, so the page says it in the
 * same words — but with the problem's `code` (and the problem itself) kept on
 * a validation refusal too, which the shared client drops. An answer without a problem's words has
 * an empty message and its `status`.
 */
export const refusalOf = async (response: Response): Promise<Error> => {
  const problem = await readJson<{
    title?: string;
    detail?: string;
    code?: string;
    errors?: Record<string, string[]>;
  }>(response).catch(() => null);
  const message = problem?.detail ?? problem?.title ?? "";
  const extra = { code: problem?.code ?? undefined, problem: problem ?? undefined };
  if (response.status === 404) return new NotFoundError(message);
  if (problem?.errors) return Object.assign(new ApiValidationError(message, problem.errors, response.status), extra);
  if (response.status === 409 && problem)
    return new ApiConflictError(message, {
      title: problem.title,
      detail: problem.detail,
      code: problem.code,
      problem,
    });
  return Object.assign(new Error(message), { status: response.status, ...extra });
};

/** A write sent past the shared client, so its refusal keeps the code (`refusalOf`). */
export const send = async <T>(url: string, init: RequestInit): Promise<T> => {
  const response = await fetch(appUrl(url), { ...init, credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (response.status === 204 ? undefined : await response.json()) as T;
  throw await refusalOf(response);
};

/** The rule a refusal names (`version_conflict`, `article_locked` …), whichever shape the client threw it in. */
export const refusalCode = (error: unknown): string | undefined => {
  if (error instanceof ApiConflictError) return error.code;
  const code = (error as { code?: unknown } | null)?.code;
  return typeof code === "string" ? code : undefined;
};

/** Whether a save was refused because the article moved on since it was read. */
export const isVersionConflict = (error: unknown): boolean => refusalCode(error) === "version_conflict";
