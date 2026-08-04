import { vi } from "vitest";

export type FetchCall = [RequestInfo | URL, RequestInit | undefined];

export const TEST_SESSION = {
  user: {
    id: "test-user",
    displayName: "Test User",
    email: "test@example.com",
    roles: ["user"],
  },
};

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const antiforgeryResponse = (token: string) => jsonResponse({ token });

const TEST_CSRF_TOKEN = "test-xsrf-token";

type SessionOption = object | null | "delegate" | Response;

export type StubbedFetch = {
  (input: RequestInfo | URL, init?: RequestInit): unknown;
  calls: FetchCall[];
  actualCalls: FetchCall[];
};

export type StubFetchOptions = {
  /** The root session bootstrap is authenticated by default for route tests. */
  session?: SessionOption;
  csrfTokens?: string[];
};

/**
 * Stubs fetch while keeping the antiforgery handshake out of business mocks.
 * The returned mock still receives the real request options, so tests can
 * assert credentials and the X-XSRF-TOKEN header on unsafe requests.
 */
export const stubFetch = (
  businessFetch?: unknown,
  { session = TEST_SESSION, csrfTokens = [TEST_CSRF_TOKEN] }: StubFetchOptions = {},
) => {
  const calls: FetchCall[] = [];
  const actualCalls: FetchCall[] = [];
  const legacyInit = (init?: RequestInit): RequestInit | undefined => {
    if (!init) return init;
    const normalizedInit = { ...init };
    delete normalizedInit.credentials;
    if (normalizedInit.headers) {
      const headers = Object.fromEntries(new Headers(normalizedInit.headers).entries());
      delete headers["x-xsrf-token"];
      const contentType = headers["content-type"];
      if (contentType) {
        delete headers["content-type"];
        headers["Content-Type"] = contentType;
      }
      if (Object.keys(headers).length > 0) normalizedInit.headers = headers;
      else delete normalizedInit.headers;
    }
    return normalizedInit;
  };
  const businessMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if (typeof businessFetch === "function") {
      return (businessFetch as (input: RequestInfo | URL, init?: RequestInit) => unknown)(input, init);
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  }) as unknown as StubbedFetch;

  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push([input, init]);
      if (String(input) === "/auth/antiforgery") {
        return Promise.resolve(antiforgeryResponse(csrfTokens.shift() ?? TEST_CSRF_TOKEN));
      }
      if (String(input) === "/auth/session" && session !== "delegate") {
        if (session instanceof Response) return Promise.resolve(session.clone());
        if (session === null) return Promise.resolve(new Response(null, { status: 401 }));
        return Promise.resolve(jsonResponse(session));
      }
      actualCalls.push([input, init]);
      return businessMock(input, legacyInit(init));
    }),
  );

  businessMock.calls = calls;
  businessMock.actualCalls = actualCalls;
  return businessMock;
};
