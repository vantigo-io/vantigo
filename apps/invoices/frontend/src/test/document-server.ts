import type { InvoiceDocument } from "../api/invoices";
import { jsonResponse, path } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";
import { meta } from "./fixtures";

/** A canned answer: a response, one still on its way, or a function making either per request. */
export type Answer = Response | Promise<Response> | (() => Response | Promise<Response>);

/**
 * The fetch fake for an issued document's page: meta (with `capabilities`
 * over the fixture's), document 1001 as `doc` says at each read, and each
 * write by "METHOD url". Anything else is a bare 404.
 */
export const documentServer = (
  doc: () => InvoiceDocument,
  answers: Record<string, Answer> = {},
  capabilities: Partial<ReturnType<typeof meta>["capabilities"]> = {},
) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, ...capabilities } }));
    }
    if (method === "GET" && url === "/api/v1/invoices/1001") return jsonResponse(200, doc());
    return new Response(null, { status: 404 });
  });

/** What `requestTo` answers for a request sent without a body — never mistaken for `{}`. */
export const NO_BODY = Symbol("no body");

/**
 * The body a write sent, found by its method and URL — never "the last
 * fetch": its parsed JSON, `NO_BODY` when it sent none, undefined when no such
 * request was made.
 */
export const requestTo = (fetchMock: StubbedFetch, method: string, url: string) => {
  const call = fetchMock.actualCalls.find(([u, init]) => path(u) === url && (init?.method ?? "GET") === method);
  if (!call) return undefined;
  const body = call[1]?.body;
  return body === undefined || body === null ? NO_BODY : JSON.parse(String(body));
};

/** How many times `url` was read (GET), so a test can tell the page read a document again after a write. */
export const readsOf = (fetchMock: StubbedFetch, url: string) =>
  fetchMock.actualCalls.filter(([u, init]) => path(u) === url && (init?.method ?? "GET") === "GET").length;

/**
 * A response the test holds back: `response` is what the fake answers with,
 * pending until `answer` is called — so a test can see a button disabled
 * while its request is on its way.
 */
export const pendingResponse = () => {
  let answer: (response: Response) => void = () => {};
  const response = new Promise<Response>((resolve) => {
    answer = resolve;
  });
  return { response, answer: (r: Response) => answer(r) };
};
