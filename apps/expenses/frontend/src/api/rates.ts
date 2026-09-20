import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * One dated rate. The rate in force on a date is the row of that kind with
 * the greatest `validFrom` on or before it — `rateOn` in `lib/rates.ts`.
 */
export type ExpenseRate = Schemas["ExpensesRateResponse"];

/**
 * Every rate the caller may read. A caller without `expenses:manage` reads
 * every kind except `mileage_customer` — what the company charges its
 * customer per kilometre is a commercial price — which is exactly why the
 * mileage form can preview what a line will pay without an administrator's
 * rights.
 */
export const expenseRatesQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "rates"],
    queryFn: ({ signal }) => request<ExpenseRate[]>("/api/v1/expenses/rates", { signal }),
  });
