import { vi } from "vitest";

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A coded refusal as the purchasing API answers it (PurchasingProblem), with the server's English, which the page never shows. */
export const refusal = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  new Response(
    JSON.stringify({ type: "about:blank", title: "Refused", status, code, detail: "The server's English.", ...extra }),
    { status, headers: { "Content-Type": "application/problem+json" } },
  );

export interface Call {
  method: string;
  url: string;
  body: unknown;
}

/** A JSON body as its value; another (a CSV price list) as its text. */
const parse = (text: string): unknown => {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
};

type Handler = (call: Call) => Response | Promise<Response> | undefined;

/**
 * A fetch fake: each request is recorded and handed to the handler, which
 * answers it or leaves it a 404. `calls` keeps every request in order;
 * `sent(method, url)` finds the body of the last one.
 */
export const fakeServer = (handler: Handler) => {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const call: Call = {
        method: init?.method ?? "GET",
        url: String(input),
        body: init?.body ? parse(String(init.body)) : undefined,
      };
      calls.push(call);
      return (await handler(call)) ?? new Response(null, { status: 404 });
    }),
  );
  return {
    calls,
    sent: (method: string, url: string | RegExp) =>
      calls.filter((c) => c.method === method && (typeof url === "string" ? c.url === url : url.test(c.url))).at(-1),
    count: (method: string, url: string | RegExp) =>
      calls.filter((c) => c.method === method && (typeof url === "string" ? c.url === url : url.test(c.url))).length,
  };
};
