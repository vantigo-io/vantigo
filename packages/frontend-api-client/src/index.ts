export type ApiError = Error & {
  status?: number;
  fields?: Record<string, string[]>;
  code?: string;
};

export type RequestOptions = RequestInit & {
  /** Expected 401 responses do not expire the current application session. */
  handleUnauthorized?: boolean;
};

export type ApiClientOptions = {
  /** Resolves an app-root-relative URL, for example against an app base path. */
  resolveUrl?: (url: string) => string;
  /** Applies application-specific API routing before URL resolution. */
  transformUrl?: (url: string) => string;
  /** Keeps session bootstrap's 404 response as an expired-session error. */
  sessionNotFoundMeansExpired?: boolean;
};

export type ApiClient = {
  request: <T>(url: string, init?: RequestOptions) => Promise<T>;
  setAuthStateClearer: (clearer: (() => void | Promise<void>) | undefined) => void;
  setUnauthorizedHandler: (handler: (() => void | Promise<void>) | undefined) => void;
};

export class ApiValidationError extends Error {
  readonly errors: Record<string, string[]>;
  readonly status: number;

  constructor(title: string, errors: Record<string, string[]>, status = 400) {
    super(title);
    this.name = "ApiValidationError";
    this.errors = errors;
    this.status = status;
  }

  get fields(): Record<string, string[]> {
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

const defaultOptions: Required<ApiClientOptions> = {
  resolveUrl: (url) => url,
  transformUrl: (url) => url,
  sessionNotFoundMeansExpired: true,
};

/** Reads a response body without consuming the response passed by the caller. */
export const readJson = async <T = unknown>(response: Response): Promise<T> => {
  return (await response.clone().json()) as T;
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

const asProblemDetails = (problem: unknown): ProblemDetails =>
  problem && typeof problem === "object" ? (problem as ProblemDetails) : {};

const problemMessage = (problem: unknown, status: number) => {
  const details = asProblemDetails(problem);
  const message = details.error?.message ?? details.detail ?? details.title;
  return typeof message === "string" ? message : `Request failed (HTTP ${status})`;
};

const apiError = (message: string, status: number, fields?: Record<string, string[]>, code?: string): ApiError =>
  Object.assign(new Error(message), { status, fields, code });

export const createApiClient = (clientOptions: ApiClientOptions = {}): ApiClient => {
  const options = { ...defaultOptions, ...clientOptions };
  let onUnauthorized: (() => void | Promise<void>) | undefined;
  let clearAuthState: () => void | Promise<void> = () => undefined;

  const requestUrl = (url: string) => {
    const transformedUrl = options.transformUrl(url);
    return transformedUrl.startsWith("/") ? options.resolveUrl(transformedUrl) : transformedUrl;
  };

  const request = async <T>(url: string, init: RequestOptions = {}): Promise<T> => {
    const { handleUnauthorized = true, ...fetchInit } = init;
    const headers = new Headers(fetchInit.headers);
    const response = await fetch(requestUrl(url), {
      ...fetchInit,
      headers,
      credentials: "include",
    });
    if (response.status === 401 && handleUnauthorized) {
      try {
        await clearAuthState();
      } finally {
        await onUnauthorized?.();
      }
    }
    if (response.ok) return response.status === 204 ? (undefined as T) : await readJson<T>(response);

    const problem = await readJson(response).catch(() => null);
    const details = asProblemDetails(problem);
    if (options.sessionNotFoundMeansExpired && url === "/api/v1/identity/session" && response.status === 404) {
      throw apiError("Your session has expired", 401);
    }
    if (response.status === 404) throw new NotFoundError(problemMessage(problem, response.status));
    const fields = normalizeFields(details.error?.fields ?? details.errors);
    if ((response.status === 400 || response.status === 409) && fields) {
      throw new ApiValidationError(problemMessage(problem, response.status), fields, response.status);
    }
    throw apiError(
      problemMessage(problem, response.status),
      response.status,
      fields,
      typeof details.error?.code === "string" ? details.error.code : undefined,
    );
  };

  return {
    request,
    setAuthStateClearer: (clearer) => {
      clearAuthState = clearer ?? (() => undefined);
    },
    setUnauthorizedHandler: (handler) => {
      onUnauthorized = handler;
    },
  };
};
