import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

/**
 * Everything the app may do, answered by the server rather than re-derived
 * here (design §5): whether this installation has projects at all, the
 * currency a new expense starts in, the categories, the period lock, the
 * receipt threshold and the caller's own three permissions.
 *
 * `defaultMarkupPercent` is present only for `expenses:manage` and no form in
 * this package sends a markup, so nothing here reads it.
 */
export type ExpensesMeta = Schemas["ExpensesMetaResponse"];
export type ExpensesMetaCapabilities = Schemas["ExpensesMetaCapabilities"];

/** A category as `/meta` carries it — the same shape `api/categories.ts` reads and writes. */
export type { ExpenseCategory } from "./categories";

export const expensesMetaQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<ExpensesMeta>("/api/v1/expenses/meta", { signal }),
  });

/**
 * Whether a day falls before the period lock. It is only ever a hint for the
 * date picker: what a caller may actually do with an expense is on the
 * expense's own `capabilities`, and `expenses:manage` is never held back.
 */
export const isBeforeLock = (date: string, meta: ExpensesMeta | undefined): boolean =>
  Boolean(meta?.lockedBefore && date < meta.lockedBefore);
