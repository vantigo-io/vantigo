import { vi } from "vitest";

export const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A coded refusal as the orders API answers it (OrdersProblem), with the server's English, which the page never shows. */
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
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
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

/** The customers the fakes know. */
export const listedCustomers = [
  { id: 42, name: "Acme AS", customerNumber: 10001, status: "active" },
  { id: 43, name: "Bygg AS", customerNumber: 10002, status: "active" },
];

/**
 * What every editor reads beside its document, answered empty unless the
 * test says otherwise: the stored PDFs, the sends, the change log, the
 * settings, the templates, and the other modules' lists the pickers read.
 */
export const commonAnswers = (call: Call, settings?: unknown): Response | undefined => {
  if (call.method !== "GET") return undefined;
  const url = call.url;
  if (/\/(documents|mailings|changes)$/.test(url)) return jsonResponse(200, { items: [] });
  if (url === "/api/v1/orders/settings" && settings) return jsonResponse(200, settings);
  if (url.startsWith("/api/v1/orders/templates")) return jsonResponse(200, { items: [] });
  if (url === "/api/v1/invoices/vat-codes")
    return jsonResponse(200, [
      { id: 3, code: "3", name: "Utgående mva", active: true, rates: [{ ratePercent: 25, validFrom: "2016-01-01" }] },
      { id: 5, code: "5", name: "Fritatt", active: true, rates: [{ ratePercent: 0, validFrom: "2016-01-01" }] },
    ]);
  if (/^\/api\/v1\/customers\/\d+\/addresses$/.test(url))
    return jsonResponse(200, {
      data: [
        {
          id: 7,
          type: "delivery",
          isPrimary: true,
          label: "Lager",
          line1: "Lagerveien 2",
          postalCode: "2000",
          city: "Lillestrøm",
          country: "NO",
        },
        {
          id: 8,
          type: "delivery",
          isPrimary: false,
          label: "Butikk",
          line1: "Torget 3",
          postalCode: "0150",
          city: "Oslo",
          country: "NO",
        },
      ],
    });
  if (/^\/api\/v1\/customers\/\d+\/contacts$/.test(url))
    return jsonResponse(200, {
      data: [{ contact: { id: 9, firstName: "Ola", lastName: "Kunde", email: "ola@acme.no" }, email: "ola@acme.no" }],
    });
  if (url.startsWith("/api/v1/customers?")) {
    const search = (new URL(url, "http://localhost").searchParams.get("search") ?? "").toLowerCase();
    return jsonResponse(200, { data: listedCustomers.filter((c) => !search || c.name.toLowerCase().includes(search)) });
  }
  if (url.startsWith("/api/v1/projects?")) return jsonResponse(200, { data: [] });
  if (url.startsWith("/api/v1/products?"))
    return jsonResponse(200, {
      data: [
        {
          id: 30,
          name: "Kabel",
          variants: [{ id: 302, sku: "KAB-20", unit: "stk", optionValues: { lengde: "20 m" } }],
        },
      ],
    });
  return undefined;
};
