import { appUrl } from "@vantigo/frontend-shell";

let csrfToken: string | null = null;
let csrfTokenRequest: Promise<string> | undefined;
let csrfGeneration = 0;
let onUnauthorized: (() => void) | undefined;
let clearAuthState: () => void | Promise<void> = () => undefined;

export type ApiError = Error & { status?: number; fields?: Record<string, string[]>; code?: string };
export type RequestOptions = RequestInit & { handleUnauthorized?: boolean };

export class ApiValidationError extends Error {
  readonly errors: Record<string, string[]>;
  readonly status: number;

  constructor(title: string, errors: Record<string, string[]>, status = 400) {
    super(title);
    this.name = "ApiValidationError";
    this.errors = errors;
    this.status = status;
  }

  get fields() {
    return this.errors;
  }

  get fieldErrors(): Record<string, string> {
    return Object.fromEntries(Object.entries(this.errors).map(([field, messages]) => [field, messages[0]]));
  }
}

export class NotFoundError extends Error {
  readonly status = 404;

  constructor(message: string) {
    super(message);
    this.name = "NotFoundError";
  }
}

type ProblemDetails = {
  error?: { code?: unknown; message?: unknown; fields?: unknown };
  detail?: unknown;
  title?: unknown;
  errors?: unknown;
};

export const setUnauthorizedHandler = (handler: (() => void) | undefined) => {
  onUnauthorized = handler;
};

export const setAuthStateClearer = (clearer: (() => void | Promise<void>) | undefined) => {
  clearAuthState = clearer ?? (() => undefined);
};

export const getCsrfToken = () => csrfToken;

export async function ensureCsrfToken(): Promise<string> {
  if (csrfToken) return csrfToken;
  if (csrfTokenRequest) return csrfTokenRequest;
  const generation = csrfGeneration;
  const promise = (async () => {
    const response = await fetch(appUrl("/api/v1/identity/antiforgery"), { credentials: "include" });
    if (!response.ok) throw new Error("Could not establish a secure session");
    const token = (await response.json()).token;
    if (typeof token !== "string" || token.length === 0 || generation !== csrfGeneration) {
      throw new Error("Could not establish a secure session");
    }
    csrfToken = token;
    return token;
  })();
  const tracked = promise.finally(() => {
    if (csrfTokenRequest === tracked) csrfTokenRequest = undefined;
  });
  csrfTokenRequest = tracked;
  return tracked;
}

export const clearCsrfToken = () => {
  csrfGeneration += 1;
  csrfToken = null;
  csrfTokenRequest = undefined;
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

const problemMessage = (problem: unknown, status: number) => {
  const details = problem && typeof problem === "object" ? (problem as ProblemDetails) : {};
  const message = details.error?.message ?? details.detail ?? details.title;
  return typeof message === "string" ? message : `Request failed (HTTP ${status})`;
};

export async function request<T>(url: string, init: RequestOptions = {}): Promise<T> {
  const { handleUnauthorized = true, ...fetchInit } = init;
  const method = (fetchInit.method ?? "GET").toUpperCase();
  const headers = new Headers(fetchInit.headers);
  if (method !== "GET" && method !== "HEAD" && method !== "OPTIONS")
    headers.set("X-XSRF-TOKEN", await ensureCsrfToken());
  const response = await fetch(url.startsWith("/") ? appUrl(url) : url, {
    ...fetchInit,
    headers,
    credentials: "include",
  });
  if (response.status === 401 && handleUnauthorized) {
    clearCsrfToken();
    try {
      await clearAuthState();
    } finally {
      onUnauthorized?.();
    }
  }
  if (response.ok) return response.status === 204 ? (undefined as T) : ((await response.clone().json()) as T);
  const problem = await response
    .clone()
    .json()
    .catch(() => null);
  const details = problem && typeof problem === "object" ? (problem as ProblemDetails) : {};
  if (response.status === 404) throw new NotFoundError(problemMessage(problem, response.status));
  const fields = normalizeFields(details.error?.fields ?? details.errors);
  if ((response.status === 400 || response.status === 409) && fields) {
    throw new ApiValidationError(problemMessage(problem, response.status), fields, response.status);
  }
  throw Object.assign(new Error(problemMessage(problem, response.status)), {
    status: response.status,
    fields,
    code: typeof details.error?.code === "string" ? details.error.code : undefined,
  }) as ApiError;
}
