import { jsonResponse, path } from "./api";
import type { Answer } from "./document-server";
import { stubFetch } from "./fetch";
import { billingRun, billingRunDetail, meta, pageOf } from "./fixtures";

export interface BillingServerOptions {
  /** Each write, or a read answered otherwise, by "METHOD path" — the path without its query, or with it. */
  answers?: Record<string, Answer>;
  /** meta's capabilities over the fixture's. */
  capabilities?: Partial<ReturnType<typeof meta>["capabilities"]>;
  /** Run 7 as GET /billing-runs/7 answers it. */
  run?: ReturnType<typeof billingRun>;
}

/**
 * The fetch fake for Billing runs: meta, the runs (run 7 and a finished run
 * 6), run 7 with its items filtered by the query's status, and a new run's
 * POST answered 201 with run 8. Each write, or any read the test answers
 * otherwise, by "METHOD url" (the full URL first, then the path alone).
 * Anything else is a bare 404.
 */
export const billingServer = ({ answers = {}, capabilities = {}, run = billingRun() }: BillingServerOptions = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const [pathname, search = ""] = url.split("?");
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`] ?? answers[`${method} ${pathname}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (method === "POST" && pathname === "/api/v1/invoices/billing-runs") {
      return jsonResponse(201, billingRun({ id: 8, status: "queued", startedAt: undefined }));
    }
    if (method !== "GET") return new Response(null, { status: 404 });
    const query = new URLSearchParams(search);
    switch (pathname) {
      case "/api/v1/invoices/meta":
        return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, ...capabilities } }));
      case "/api/v1/invoices/billing-runs":
        return jsonResponse(
          200,
          pageOf([
            billingRun(),
            billingRun({
              id: 6,
              through: "2026-07-31",
              issue: false,
              status: "done",
              customers: 2,
              counts: { queued: 0, drafted: 2, issued: 0, skipped: 0, failed: 0, cancelled: 0 },
              createdAt: "2026-08-12T08:00:00Z",
              finishedAt: "2026-08-12T08:01:00Z",
            }),
          ]),
        );
      case "/api/v1/invoices/billing-runs/7":
        return jsonResponse(200, billingRunDetail(query.get("status"), run));
      default:
        return new Response(null, { status: 404 });
    }
  });
