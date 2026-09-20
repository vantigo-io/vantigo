import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * The caller's own key figures, for the strip above their expense list. Every
 * status is always present, 0 included; `unreimbursed` is one line per
 * currency and is empty when nothing is owed. Nothing is ever converted.
 */
export type ExpenseStats = Schemas["ExpensesStatsResponse"];
export type ExpenseCurrencyAmount = Schemas["ExpensesCurrencyAmount"];

export const expenseStatsQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "stats"],
    queryFn: ({ signal }) => request<ExpenseStats>("/api/v1/expenses/stats", { signal }),
  });
