import type { StubbedFetch } from "./fetch";

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A refusal in the shape the API answers it: a problem, with field errors when it has any. */
export const problemResponse = (status: number, title: string, extra: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ title, status, ...extra }), {
    status,
    headers: { "Content-Type": "application/problem+json" },
  });

/** Every business request with this method, its URL, headers and parsed JSON body. */
export const sentAll = (fetchMock: StubbedFetch, method: string) =>
  fetchMock.actualCalls
    .filter(([, init]) => (init?.method ?? "GET") === method)
    .map(([url, init]) => ({
      url: String(url),
      headers: Object.fromEntries(new Headers(init?.headers).entries()),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    }));

/** The first business request with this method. */
export const sent = (fetchMock: StubbedFetch, method: string) =>
  sentAll(fetchMock, method)[0] ?? { url: undefined, headers: {}, body: undefined };
