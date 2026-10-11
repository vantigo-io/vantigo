import { jsonResponse, path } from "./api";
import type { Answer } from "./document-server";
import { stubFetch } from "./fetch";
import {
  awaitingPrint,
  meta,
  overdueList,
  pageOf,
  printBatch,
  reminderRun,
  reminderRunDetail,
  runPreview,
  runResult,
} from "./fixtures";

export interface OverdueServerOptions {
  /** Each write, or a read answered otherwise, by "METHOD path" — the path without its query, or with it. */
  answers?: Record<string, Answer>;
  /** meta's capabilities over the fixture's. */
  capabilities?: Partial<ReturnType<typeof meta>["capabilities"]>;
  /** The answer to every GET /overdue, given its query. */
  overdue?: (query: URLSearchParams) => ReturnType<typeof overdueList> | Response;
  /** The answer to a POST /reminder-runs, given its body: the preview's (dryRun) and the run's alike. */
  runs?: (body: Record<string, unknown>) => Response;
}

/**
 * The fetch fake for the Overdue area: meta, the overdue list, the run's
 * preview and the run (POST /reminder-runs, by its body's dryRun), the runs
 * and run 11, the letters awaiting print, and the print batches with batch
 * 7. Each write, or any read the test answers otherwise, by "METHOD url" (the
 * full URL first, then the path alone). Anything else is a bare 404.
 */
export const overdueServer = ({ answers = {}, capabilities = {}, overdue, runs }: OverdueServerOptions = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const [pathname, search = ""] = url.split("?");
    const method = init?.method ?? "GET";
    const answer = answers[`${method} ${url}`] ?? answers[`${method} ${pathname}`];
    if (answer) return typeof answer === "function" ? answer() : answer;
    if (method === "POST" && pathname === "/api/v1/invoices/reminder-runs") {
      const body = JSON.parse(String(init?.body ?? "{}")) as Record<string, unknown>;
      if (runs) return runs(body);
      return body.dryRun ? jsonResponse(200, runPreview()) : jsonResponse(201, runResult());
    }
    if (method !== "GET") return new Response(null, { status: 404 });
    const query = new URLSearchParams(search);
    switch (pathname) {
      case "/api/v1/invoices/meta":
        return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, ...capabilities } }));
      case "/api/v1/invoices/overdue": {
        const list = overdue ? overdue(query) : overdueList();
        return list instanceof Response ? list : jsonResponse(200, list);
      }
      case "/api/v1/invoices/reminder-runs":
        return jsonResponse(200, pageOf([reminderRun()]));
      case "/api/v1/invoices/reminder-runs/11":
        return jsonResponse(200, reminderRunDetail());
      case "/api/v1/invoices/reminders":
        return jsonResponse(200, pageOf(query.get("status") === "awaiting_print" ? awaitingPrint() : []));
      case "/api/v1/invoices/reminder-print-batches":
        return jsonResponse(200, pageOf([printBatch()]));
      default:
        return new Response(null, { status: 404 });
    }
  });
