import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { type ApiError, EXPENSES_QUERY_KEY, NotFoundError, request } from "./request";

type Schemas = components["schemas"];

/**
 * What one project's expenses cost it and bill its customer, in sum. The
 * figures are the very ones `contracts.ProjectExpenses` answers the projects
 * module with, so the Expenses tab and the project's own Economy tab can
 * never show two different numbers.
 */
export type ProjectExpensesSummary = Schemas["ExpensesProjectSummaryResponse"];

/**
 * One currency's figures. A line carries **its own** currency, which may be
 * neither its travel claim's nor its project's, so nothing here is ever
 * converted and two currencies are never added together.
 */
export type ProjectExpensesCurrency = Schemas["ExpensesProjectSummaryCurrency"];

/**
 * One status bucket: how many lines, what they cost the project (the net,
 * whoever paid) and what the billable ones bill. Each figure was rounded once
 * from its own unrounded sum, so the three buckets must **never** be added up
 * to make the total — `total` is carried for exactly that reason.
 */
export type ProjectExpensesBucket = Schemas["ExpensesProjectSummaryBucket"];

export type ProjectExpensesCapabilities = Schemas["ExpensesProjectSummaryCapabilities"];

/**
 * The part of one currency's buckets that is supplier invoices (supplier
 * invoices design D3) — a line of its own beneath the total, never a split of
 * it. Absent when the currency holds none.
 */
export type ProjectExpensesSupplierInvoices = Schemas["ExpensesProjectSummarySupplierInvoices"];

/**
 * Whether a failed read is worth asking again.
 *
 * A 4xx is the server's **answer** — this is not yours, that query
 * contradicts itself — and asking three more times over seven seconds of
 * backoff only delays the sentence it already gave, with the page showing a
 * stale or empty state in the meantime. A 5xx or a dropped connection is a
 * different thing, and keeps the default two retries.
 */
export const notAfterARefusal = (count: number, error: Error): boolean => {
  if (error instanceof NotFoundError) return false;
  const status = (error as ApiError).status;
  if (typeof status === "number" && status >= 400 && status < 500) return false;
  return count < 2;
};

/**
 * One project's expense totals.
 *
 * A **bare 404** is the answer for three deliberately indistinguishable
 * reasons — no projects module, no such project, or no financial rights on it
 * — so a caller can neither tell them apart nor probe for one. The tab reads
 * it as "these figures are not yours", not as a failure, and shows the list
 * underneath either way.
 *
 * It is not retried past a refusal: a 404 is an ordinary answer here, and
 * three attempts before the tab can draw would only delay it.
 */
export const projectExpensesSummaryQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "projects", "summary", projectId],
    queryFn: ({ signal }) =>
      request<ProjectExpensesSummary>(`/api/v1/expenses/projects/${projectId}/summary`, { signal }),
    retry: notAfterARefusal,
  });
