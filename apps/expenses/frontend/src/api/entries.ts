import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { ExpenseKind, ExpenseStatus, PaidBy } from "../lib/status";
import { EXPENSES_QUERY_KEY, json, request } from "./request";

export type { ExpenseKind, ExpenseStatus, PaidBy } from "../lib/status";
export { ApiValidationError, NotFoundError } from "./request";

type Schemas = components["schemas"];

/**
 * One expense as the caller may see it. The contract declares its
 * enumerations as plain strings (OpenAPI 3.0 without enum members), so they
 * are narrowed here to the exact values the backend answers; everything else
 * is the generated shape.
 */
export type Expense = Omit<Schemas["ExpensesEntryResponse"], "kind" | "status" | "paidBy"> & {
  kind: ExpenseKind;
  status: ExpenseStatus;
  paidBy?: PaidBy;
};

export type ExpenseCapabilities = Schemas["ExpensesEntryCapabilities"];
export type ExpenseAttachment = Schemas["ExpensesAttachmentResponse"];
export type ExpenseBilling = Schemas["ExpensesEntryBilling"];
export type ExpenseDecision = Schemas["ExpensesEntryDecision"];
export type ExpenseReimbursement = Schemas["ExpensesEntryReimbursement"];
export type ExpenseRateOverride = Schemas["ExpensesEntryRateOverride"];
export type ExpenseOwner = Schemas["ExpensesEntryOwner"];
export type PaginationMetadata = Schemas["PaginationMetadata"];

export type ExpenseInput = Schemas["ExpensesEntryRequest"];

/** A replace carries the revision the entry was read at; a stale one is a 409. */
export type ExpenseUpdateInput = Schemas["ExpensesEntryUpdateRequest"];

export interface PaginatedResponse<T> {
  data: T[];
  pagination: PaginationMetadata;
}

/**
 * What `GET /expenses/entries` may be narrowed by. An absent filter is no
 * filter at all — and the list is never wider than what the caller may see,
 * so `userId` only ever narrows it.
 */
export interface ExpenseFilters {
  userId?: string;
  projectId?: number;
  status?: ExpenseStatus;
  kind?: ExpenseKind;
  from?: string;
  to?: string;
  reimbursed?: boolean;
  page?: number;
  pageSize?: number;
}

const listQuery = (filters: ExpenseFilters): string => {
  const query = new URLSearchParams();
  if (filters.userId) query.set("userId", filters.userId);
  if (filters.projectId !== undefined) query.set("projectId", String(filters.projectId));
  if (filters.status) query.set("status", filters.status);
  if (filters.kind) query.set("kind", filters.kind);
  if (filters.from) query.set("from", filters.from);
  if (filters.to) query.set("to", filters.to);
  if (filters.reimbursed !== undefined) query.set("reimbursed", String(filters.reimbursed));
  if (filters.page !== undefined) query.set("page", String(filters.page));
  if (filters.pageSize !== undefined) query.set("pageSize", String(filters.pageSize));
  const search = query.toString();
  return search ? `?${search}` : "";
};

/** One page of the expenses the caller may see, the latest day first. */
export const expensesQueryOptions = (filters: ExpenseFilters) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "entries", "list", filters],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<Expense>>(`/api/v1/expenses/entries${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const expenseQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "entries", "detail", id],
    queryFn: ({ signal }) => request<Expense>(`/api/v1/expenses/entries/${id}`, { signal }),
  });

export const createExpense = (input: ExpenseInput): Promise<Expense> =>
  request<Expense>("/api/v1/expenses/entries", json("POST", input));

/**
 * A full replace, guarded by the revision the form was opened at. Whatever is
 * left out is cleared, so the form echoes back the project link, the billing
 * line and the billable flag exactly as it loaded them. It never sends
 * `markupPercent` or `billRatePerKm`: those are the project's figures and a
 * caller without financial rights is refused on the field for naming one.
 */
export const updateExpense = (id: number, input: ExpenseUpdateInput): Promise<Expense> =>
  request<Expense>(`/api/v1/expenses/entries/${id}`, json("PUT", input));

export const deleteExpense = (id: number): Promise<void> =>
  request<void>(`/api/v1/expenses/entries/${id}`, { method: "DELETE" });

/**
 * Submits expenses for approval. All or nothing: a refusal names each
 * offending id on `entryIds` and nothing moves. `capabilities.canSubmit` is
 * optimistic — the server can still refuse for a missing rate, the receipt
 * rule or an amount that overruns — so the messages are what the caller is
 * shown.
 */
export const submitExpenses = (entryIds: number[]): Promise<Expense[]> =>
  request<Expense[]>("/api/v1/expenses/submit", json("POST", { entryIds }));
