import type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
import { ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiValidationError, NotFoundError, readJson };

/**
 * A 409 whose problem body names something this package's UI can recover
 * from: D5's revision conflict (`code` absent) or D6's duplicate legal
 * identity (`code: "duplicate_legal_identity"`, `duplicates` populated).
 * Sibling to `ApiValidationError` — a 409-specific shape rather than the
 * generic error every other status falls back to.
 */
export class ApiConflictError extends Error {
  readonly status = 409;
  readonly title?: string;
  readonly detail?: string;
  readonly code?: string;
  readonly duplicates?: { id: number; customerNumber: number; name: string; status: string }[];

  constructor(
    title: string | undefined,
    detail: string | undefined,
    code?: string,
    duplicates?: ApiConflictError["duplicates"],
  ) {
    super(detail ?? title ?? "Request failed (HTTP 409)");
    this.name = "ApiConflictError";
    this.title = title;
    this.detail = detail;
    this.code = code;
    this.duplicates = duplicates;
  }
}

let onUnauthorized: (() => void | Promise<void>) | undefined;
let clearAuthState: (() => void | Promise<void>) | undefined;

export const setUnauthorizedHandler = (handler: (() => void | Promise<void>) | undefined) => {
  onUnauthorized = handler;
};
export const setAuthStateClearer = (clearer: (() => void | Promise<void>) | undefined) => {
  clearAuthState = clearer;
};

type ProblemDetails = {
  error?: { code?: unknown; message?: unknown; fields?: unknown };
  detail?: unknown;
  title?: unknown;
  errors?: unknown;
  code?: unknown;
  duplicates?: unknown;
};

const asProblemDetails = (problem: unknown): ProblemDetails =>
  problem && typeof problem === "object" ? (problem as ProblemDetails) : {};

const problemMessage = (problem: unknown, status: number) => {
  const details = asProblemDetails(problem);
  const message = details.error?.message ?? details.detail ?? details.title;
  return typeof message === "string" ? message : `Request failed (HTTP ${status})`;
};

const normalizeFields = (fields: unknown): Record<string, string[]> | undefined => {
  if (!fields || typeof fields !== "object") return undefined;
  const normalized = Object.fromEntries(
    Object.entries(fields).flatMap(([field, messages]) => {
      const values = Array.isArray(messages) ? messages : [messages];
      const strings = values.filter((message): message is string => typeof message === "string");
      return strings.length > 0 ? [[field, strings]] : [];
    }),
  );
  return Object.keys(normalized).length > 0 ? normalized : undefined;
};

const apiError = (message: string, status: number, fields?: Record<string, string[]>, code?: string): ApiError =>
  Object.assign(new Error(message), { status, fields, code });

const SESSION_URL = "/api/v1/identity/session";

/**
 * This package's one HTTP entry point. Every other file in it imports
 * `request` from here rather than calling `fetch` itself, so the session
 * cookie, the centralized 401 handling and the problem-JSON error mapping
 * below all happen exactly once.
 *
 * It is a local implementation rather than `@vantigo/frontend-api-client`'s
 * `createApiClient().request` directly (as every sibling app's `request.ts`
 * uses it) because that shared client discards the extra fields D5's and
 * D6's 409 problem bodies carry (`code`, `duplicates`) once it has built its
 * generic error — there is no way to recover them from the thrown error to
 * build `ApiConflictError`. Reimplementing the same mapping here, with that
 * one addition, keeps every other status (401, 400, 404) behaving exactly as
 * the shared client's, proven by the tests this file already had.
 */
export async function request<T>(url: string, init: RequestOptions = {}): Promise<T> {
  const { handleUnauthorized = true, ...fetchInit } = init;
  const headers = new Headers(fetchInit.headers);
  const response = await fetch(url.startsWith("/") ? appUrl(url) : url, {
    ...fetchInit,
    headers,
    credentials: "include",
  });
  if (response.status === 401 && handleUnauthorized) {
    try {
      await clearAuthState?.();
    } finally {
      await onUnauthorized?.();
    }
  }
  if (response.ok) return response.status === 204 ? (undefined as T) : await readJson<T>(response);

  const problem = await readJson(response).catch(() => null);
  const details = asProblemDetails(problem);
  if (url === SESSION_URL && response.status === 404) {
    throw apiError("Your session has expired", 401);
  }
  if (response.status === 404) throw new NotFoundError(problemMessage(problem, response.status));

  const fields = normalizeFields(details.error?.fields ?? details.errors);
  if ((response.status === 400 || response.status === 409) && fields) {
    throw new ApiValidationError(problemMessage(problem, response.status), fields, response.status);
  }

  if (response.status === 409 && typeof details.title === "string") {
    throw new ApiConflictError(
      details.title,
      typeof details.detail === "string" ? details.detail : undefined,
      typeof details.code === "string" ? details.code : undefined,
      Array.isArray(details.duplicates) ? (details.duplicates as ApiConflictError["duplicates"]) : undefined,
    );
  }

  throw apiError(
    problemMessage(problem, response.status),
    response.status,
    fields,
    typeof details.error?.code === "string" ? details.error.code : undefined,
  );
}
