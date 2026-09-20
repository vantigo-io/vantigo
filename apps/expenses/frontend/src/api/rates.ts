import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, json, request } from "./request";

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

export type ExpenseRateInput = Schemas["ExpensesRateRequest"];
export type ExpenseRateUpdateInput = Schemas["ExpensesRateUpdateRequest"];

/** Adds a dated rate. It prices expenses saved from now on; a submitted expense's rate never moves. */
export const createExpenseRate = (input: ExpenseRateInput): Promise<ExpenseRate> =>
  request<ExpenseRate>("/api/v1/expenses/rates", json("POST", input));

/** A full replace of one row's day, value, currency and source. The kind stays the row's own. */
export const updateExpenseRate = (id: number, input: ExpenseRateUpdateInput): Promise<ExpenseRate> =>
  request<ExpenseRate>(`/api/v1/expenses/rates/${id}`, json("PUT", input));

/** Removes a row. Expenses it priced keep what was snapshotted on them. */
export const deleteExpenseRate = (id: number): Promise<void> =>
  request<void>(`/api/v1/expenses/rates/${id}`, { method: "DELETE" });

/**
 * Puts one kind back to what the product shipped: a shipped day that was
 * removed is restored, and one that was edited returns to its shipped value,
 * currency and label. The company's own rows, on days the product never
 * shipped, are untouched, and nothing is ever removed. Answers the whole
 * table as it now stands.
 */
export const resetExpenseRateKind = (kind: string): Promise<ExpenseRate[]> =>
  request<ExpenseRate[]>("/api/v1/expenses/rates/reset", json("POST", { kind }));
