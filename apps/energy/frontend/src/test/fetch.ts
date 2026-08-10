import { vi } from "vitest";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

export const stubFetch = (businessFetch: (input: RequestInfo | URL, init?: RequestInit) => unknown) => {
  const businessMock = vi.fn(businessFetch);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/v1/identity/antiforgery")
        return Promise.resolve(jsonResponse({ token: "test-token" }));
      return businessMock(input, init);
    }),
  );
  return businessMock;
};
