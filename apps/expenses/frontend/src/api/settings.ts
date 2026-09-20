import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The installation's expense settings. Every `expenses:access` holder may
 * read them — the lock and the receipt rule decide what they may record — but
 * `defaultMarkupPercent` is present only for `expenses:manage`, because what
 * the company adds to a supplier cost before invoicing it on is a commercial
 * figure.
 */
export type ExpenseSettings = Schemas["ExpensesSettingsResponse"];
export type ExpenseSettingsInput = Schemas["ExpensesSettingsRequest"];

export const expenseSettingsQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "settings"],
    queryFn: ({ signal }) => request<ExpenseSettings>("/api/v1/expenses/settings", { signal }),
  });

/**
 * A full replace. Leaving `lockedBefore` or `receiptRequiredOver` out clears
 * it — which is how the receipt rule is turned off, and is a different thing
 * from sending 0, which means "every employee-paid outlay needs a receipt".
 */
export const updateExpenseSettings = (input: ExpenseSettingsInput): Promise<ExpenseSettings> =>
  request<ExpenseSettings>("/api/v1/expenses/settings", json("PUT", input));
