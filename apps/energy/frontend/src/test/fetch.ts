import { vi } from "vitest";

export const stubFetch = (businessFetch: (input: RequestInfo | URL, init?: RequestInit) => unknown) => {
  const businessMock = vi.fn(businessFetch);
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => businessMock(input, init)),
  );
  return businessMock;
};
