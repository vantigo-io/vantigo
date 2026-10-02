import type { StubbedFetch } from "./fetch";

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A fetch input as the URL string the fakes match on. */
export const path = (input: RequestInfo | URL) => String(input);

/**
 * A coded refusal as the server answers it: the invoices conflict problem — a
 * 409, a 502 or a 503 alike — with the server's English detail, which the
 * page must never show, and whatever the refusal names (`extra`).
 */
export const refusal = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  jsonResponse(status, {
    type: "about:blank",
    title: "Refused",
    status,
    code,
    detail: "The server's English.",
    ...extra,
  });

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

/** A customer as the customers module's list answers one: the four fields the picker reads. */
export interface ListedCustomer {
  id: number;
  name: string;
  customerNumber: number;
  status: string;
}

/** The customers the fakes know: three active, one archived. */
export const listedCustomers: ListedCustomer[] = [
  { id: 2001, name: "Acme AS", customerNumber: 10001, status: "active" },
  { id: 2002, name: "Kari Nordmann", customerNumber: 10002, status: "active" },
  { id: 2003, name: "Bygg AS", customerNumber: 10003, status: "active" },
  { id: 2004, name: "Gammel Handel AS", customerNumber: 10004, status: "archived" },
];

/**
 * GET /api/v1/customers as the customers module answers it, for the rules the
 * picker meets: `search` matches a name case-insensitively or a customer
 * number whole — never a label like "Acme AS (10001)" — `status` shows that
 * status only, and archived customers only with `includeArchived=true`.
 */
export const customerSearch = (url: string, customers: ListedCustomer[] = listedCustomers) => {
  const query = new URL(url, "http://localhost").searchParams;
  const search = (query.get("search") ?? "").toLowerCase();
  const status = query.get("status");
  const archived = query.get("includeArchived") === "true";
  const data = customers.filter(
    (c) =>
      (status ? c.status === status : archived || c.status !== "archived") &&
      (!search || c.name.toLowerCase().includes(search) || String(c.customerNumber) === search),
  );
  return jsonResponse(200, { data });
};
