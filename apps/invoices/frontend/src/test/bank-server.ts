import type { BankTransaction } from "../api/bank";
import { jsonResponse, path } from "./api";
import type { Answer } from "./document-server";
import { stubFetch } from "./fetch";
import { bankAccount, bankFile, bankTransaction, meta, pageOf } from "./fixtures";

export interface BankServerOptions {
  /** Each write, or a read answered otherwise, by "METHOD path" — the path without its query, or with it. */
  answers?: Record<string, Answer>;
  /** meta's capabilities over the fixture's. */
  capabilities?: Partial<ReturnType<typeof meta>["capabilities"]>;
  /**
   * The answer to every GET /bank-transactions, given its query: the lines
   * (one page of them), a whole page with its own paging, or a response.
   */
  lines?: (query: URLSearchParams) => BankTransaction[] | ReturnType<typeof pageOf<BankTransaction>> | Response;
}

/**
 * The fetch fake for the Payments area: meta, one account, one file — 1001,
 * its detail with the fixture's line — and the queue's lines; each write, or
 * any read the test answers otherwise, by "METHOD url" (the full URL first,
 * then the path alone). Anything else is a bare 404.
 */
export const bankServer = ({ answers = {}, capabilities = {}, lines }: BankServerOptions = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const [pathname, search = ""] = url.split("?");
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`] ?? answers[`${method} ${pathname}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (method !== "GET") return new Response(null, { status: 404 });
    switch (pathname) {
      case "/api/v1/invoices/meta":
        return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, ...capabilities } }));
      case "/api/v1/invoices/bank-accounts":
        return jsonResponse(200, { data: [bankAccount()] });
      case "/api/v1/invoices/bank-files":
        return jsonResponse(200, pageOf([bankFile()]));
      case "/api/v1/invoices/bank-files/1001":
        return jsonResponse(200, { file: bankFile(), transactions: [bankTransaction()] });
      case "/api/v1/invoices/bank-transactions": {
        const answer = lines ? lines(new URLSearchParams(search)) : [bankTransaction()];
        if (answer instanceof Response) return answer;
        return jsonResponse(200, Array.isArray(answer) ? pageOf(answer) : answer);
      }
      default:
        return new Response(null, { status: 404 });
    }
  });
