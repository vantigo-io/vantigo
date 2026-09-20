import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * A project the caller may book an expense on, with the active billing lines
 * they may book it against — so the expense form needs no code of the
 * projects module's own, and the picker cannot offer something the save would
 * refuse.
 */
export type ExpenseProjectOption = Schemas["ExpensesProjectOption"];
export type ExpenseProjectBillingLine = Schemas["ExpensesProjectOptionBillingLine"];

/**
 * Only ever asked for when `meta.projectsAvailable` is true: without the
 * projects module the endpoint answers 404, exactly as a path that is not
 * there, and there is no project block to draw.
 */
export const expenseProjectsQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "projects"],
    queryFn: ({ signal }) => request<ExpenseProjectOption[]>("/api/v1/expenses/projects", { signal }),
  });
