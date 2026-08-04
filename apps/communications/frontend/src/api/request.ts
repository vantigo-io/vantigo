let onUnauthorized: (() => void) | undefined;
let csrfToken: string | null = null;
let csrfRequest: Promise<string> | undefined;
export type ApiError = Error & { status?: number };
export type RequestOptions = RequestInit & { handleUnauthorized?: boolean };
export const setUnauthorizedHandler = (handler: (() => void) | undefined) => {
  onUnauthorized = handler;
};
export const clearCsrfToken = () => {
  csrfToken = null;
  csrfRequest = undefined;
};
export async function ensureCsrfToken(): Promise<string> {
  if (csrfToken) return csrfToken;
  if (!csrfRequest)
    csrfRequest = fetch("/auth/antiforgery", { credentials: "include" })
      .then(async (response) => {
        const body = await response.json().catch(() => ({}));
        if (!response.ok || typeof body.token !== "string" || !body.token)
          throw new Error(body?.error?.message || "Could not establish a secure session");
        csrfToken = body.token;
        return body.token as string;
      })
      .finally(() => {
        csrfRequest = undefined;
      });
  return csrfRequest as Promise<string>;
}
export async function request<T>(url: string, init: RequestOptions = {}): Promise<T> {
  const { handleUnauthorized = true, ...fetchInit } = init;
  const method = (fetchInit.method || "GET").toUpperCase();
  const headers = new Headers(fetchInit.headers);
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) headers.set("X-XSRF-TOKEN", await ensureCsrfToken());
  const response = await fetch(url, { ...fetchInit, headers, credentials: "include" });
  if (response.status === 401 && handleUnauthorized) {
    clearCsrfToken();
    onUnauthorized?.();
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    const error: ApiError = Object.assign(
      new Error(body?.error?.message || body?.detail || body?.title || `Request failed (HTTP ${response.status})`),
      { status: response.status },
    );
    throw error;
  }
  return response.status === 204 ? (undefined as T) : ((await response.json()) as T);
}
