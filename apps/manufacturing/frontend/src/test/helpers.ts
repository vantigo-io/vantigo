import type { StubbedFetch } from "./fetch";
import { parseBody } from "./server";

/** The requests the fake received, matching a method and a path, with their bodies. */
export const sent = (fetchMock: StubbedFetch, method: string, pattern: RegExp) =>
  fetchMock.actualCalls
    .filter(([url, init]) => (init?.method ?? "GET") === method && pattern.test(String(url)))
    .map(([url, init]) => ({ url: String(url), body: parseBody(init?.body) as Record<string, unknown> }));
