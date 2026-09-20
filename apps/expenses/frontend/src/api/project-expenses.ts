import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, NotFoundError, request } from "./request";

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
 * One project's expense totals.
 *
 * A **bare 404** is the answer for three deliberately indistinguishable
 * reasons — no projects module, no such project, or no financial rights on it
 * — so a caller can neither tell them apart nor probe for one. The tab reads
 * it as "these figures are not yours", not as a failure, and shows the list
 * underneath either way.
 *
 * It is not retried: a 404 is an ordinary answer here, and three attempts
 * before the tab can draw would only delay it.
 */
export const projectExpensesSummaryQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "projects", "summary", projectId],
    queryFn: ({ signal }) =>
      request<ProjectExpensesSummary>(`/api/v1/expenses/projects/${projectId}/summary`, { signal }),
    retry: (count: number, error: Error) => !(error instanceof NotFoundError) && count < 2,
  });
