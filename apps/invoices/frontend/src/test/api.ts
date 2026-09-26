import type { StubbedFetch } from "./fetch";

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A refusal in the shape the API answers it: a problem, with field errors when it has any. */
export const problemResponse = (status: number, title: string, fields?: Record<string, string[]>) =>
  jsonResponse(status, { title, status, ...(fields ? { errors: fields } : {}) });

/** Runs a `queryOptions` factory's queryFn the way TanStack Query would, without a client. */
export const runQuery = (options: { queryFn?: unknown }) =>
  (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

/** The URL and parsed JSON body of the first business request with this method. */
export const sent = (fetchMock: StubbedFetch, method: string) => {
  const [url, init] = fetchMock.actualCalls.find(([, request]) => (request?.method ?? "GET") === method) ?? [];
  return {
    url: url === undefined ? undefined : String(url),
    body: init?.body ? JSON.parse(String(init.body)) : undefined,
  };
};
