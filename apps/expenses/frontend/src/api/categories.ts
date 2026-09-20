import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { EXPENSES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * One expense category. Categories are deactivated, never deleted, once an
 * expense has used one — so the expenses booked on it keep their name.
 */
export type ExpenseCategory = Schemas["ExpensesCategoryResponse"];
export type ExpenseCategoryInput = Schemas["ExpensesCategoryRequest"];
export type ExpenseCategoryUpdateInput = Schemas["ExpensesCategoryUpdateRequest"];

/** Every category, in position order and then by name, inactive ones included. */
export const expenseCategoriesQueryOptions = () =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "categories"],
    queryFn: ({ signal }) => request<ExpenseCategory[]>("/api/v1/expenses/categories", { signal }),
  });

/** The name is unique case-insensitively; an absent position puts it last. */
export const createExpenseCategory = (input: ExpenseCategoryInput): Promise<ExpenseCategory> =>
  request<ExpenseCategory>("/api/v1/expenses/categories", json("POST", input));

/** A full replace of the name, whether it may be chosen, and where it sits. */
export const updateExpenseCategory = (id: number, input: ExpenseCategoryUpdateInput): Promise<ExpenseCategory> =>
  request<ExpenseCategory>(`/api/v1/expenses/categories/${id}`, json("PUT", input));
