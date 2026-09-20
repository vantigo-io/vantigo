import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { Expense, PaginatedResponse } from "./entries";
import { EXPENSES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * One person's submitted expenses that the caller may approve, with the
 * figures an approver decides on at a glance. `totals` is one line per
 * currency and nothing is ever converted; `receiptsMissing` counts outlays
 * with no receipt at all (mileage is never counted); `overriddenRates` counts
 * the lines an approver has already repriced.
 */
export type ExpenseApprovalGroup = Omit<Schemas["ExpensesApprovalGroup"], "entries"> & { entries: Expense[] };
export type ExpenseCurrencyTotal = Schemas["ExpensesCurrencyTotal"];
export type ExpenseUserRef = Schemas["ExpensesUserRef"];

export type RateOverrideInput = Schemas["ExpensesRateOverrideRequest"];
export type BillingInput = Schemas["ExpensesBillingRequest"];

/**
 * One billing line a pricer may book an expense against. `active` is false
 * only for the line the expense already carries after its project stopped
 * using it — the dialog has to show what is stored without offering it again.
 */
export type ExpenseBillingLineOption = Schemas["ExpensesBillingLineOption"];

/**
 * The billing lines of the expense's *own* project, judged by exactly the
 * rule `PUT /entries/{id}/billing` is judged by.
 *
 * `GET /projects` cannot serve the pricing dialog: it answers the projects the
 * **caller** may book an expense on, which is the member-or-manager right on a
 * project still open for work, while pricing belongs to whoever may see the
 * project's money. A finance person on no project team, and anybody pricing a
 * line on a completed project, would be offered nothing at all by that list —
 * and the line already stored would render as "No billing line".
 */
export const expenseBillingLinesQueryOptions = (entryId: number) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "entries", "billing-lines", entryId],
    queryFn: ({ signal }) =>
      request<ExpenseBillingLineOption[]>(`/api/v1/expenses/entries/${entryId}/billing-lines`, { signal }),
  });

/**
 * The approval queue: the submitted expenses the caller may approve, grouped
 * per person, the person who has been waiting longest first, and paged by
 * person — so a page always holds whole groups. A caller who approves nothing
 * at all gets a 403, which the page shows as an empty state rather than an
 * error.
 */
export const expenseApprovalsQueryOptions = (page: number) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "approvals", page],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<ExpenseApprovalGroup>>(`/api/v1/expenses/approvals?page=${page}`, { signal }),
    placeholderData: keepPreviousData,
  });

/**
 * Approves expenses. All or nothing: one expense that may not be approved
 * refuses the whole request with a message per offending id on `entryIds`,
 * and nothing moves.
 */
export const approveExpenses = (entryIds: number[]): Promise<Expense[]> =>
  request<Expense[]>("/api/v1/expenses/approve", json("POST", { entryIds }));

/** Rejects expenses with a reason their owner sees. 1 to 1000 characters once trimmed. */
export const rejectExpenses = (entryIds: number[], reason: string): Promise<Expense[]> =>
  request<Expense[]>("/api/v1/expenses/reject", json("POST", { entryIds, reason }));

/**
 * Returns approved expenses to a draft their owner can change and submit
 * again. Never one that has been reimbursed or invoiced — each of those
 * tracks has an undo of its own.
 */
export const unapproveExpenses = (entryIds: number[]): Promise<Expense[]> =>
  request<Expense[]>("/api/v1/expenses/unapprove", json("POST", { entryIds }));

/**
 * Replaces the reimbursement rate on one submitted mileage line and reprices
 * it. What the rate table had said is recorded beside the new rate, the first
 * time and no later, so the original is never lost.
 */
export const overrideExpenseRate = (id: number, input: RateOverrideInput): Promise<Expense> =>
  request<Expense>(`/api/v1/expenses/entries/${id}/rate`, json("PUT", input));

/**
 * Prices one expense from its project's side. A door of its own because
 * `expenses:manage` does not imply financial rights on a project and an
 * employee's expense form never carries a markup — `capabilities.canSetBilling`
 * is the gate, and the period lock does not reach it.
 */
export const setExpenseBilling = (id: number, input: BillingInput): Promise<Expense> =>
  request<Expense>(`/api/v1/expenses/entries/${id}/billing`, json("PUT", input));
