import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { Expense, FlowResult, FlowUnits, PaginatedResponse } from "./entries";
import { unitsBody } from "./entries";
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
 * Approves a selection of both kinds of unit in **one** request. All or
 * nothing across both lists: a unit that may not be approved refuses the whole
 * request with a message per offending id on the list that named it —
 * `entryIds` for a standalone expense, `claimIds` for a trip — and nothing
 * moves. An empty list is left out, because a present but empty one is a 400.
 */
export const approveUnits = (units: FlowUnits): Promise<FlowResult> =>
  request<FlowResult>("/api/v1/expenses/approve", json("POST", unitsBody(units)));

/** Rejects units with a reason their owner sees. 1 to 1000 characters once trimmed. */
export const rejectUnits = (units: FlowUnits, reason: string): Promise<FlowResult> =>
  request<FlowResult>("/api/v1/expenses/reject", json("POST", { ...unitsBody(units), reason }));

/**
 * Returns approved units to a draft their owner can change and submit again.
 * Never one that has been reimbursed, nor a trip left holding a line that has
 * been invoiced — each of those tracks has an undo of its own.
 */
export const unapproveUnits = (units: FlowUnits): Promise<FlowResult> =>
  request<FlowResult>("/api/v1/expenses/unapprove", json("POST", unitsBody(units)));

/**
 * The two, for a caller that only ever moves standalone expenses — the entry
 * drawer, which decides one at a time. Rejecting goes through `RejectModal`,
 * which takes units, so it needs no wrapper of its own.
 */
export const approveExpenses = (entryIds: number[]): Promise<FlowResult> => approveUnits({ entryIds });
export const unapproveExpenses = (entryIds: number[]): Promise<FlowResult> => unapproveUnits({ entryIds });

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

export type InvoicedInput = Schemas["ExpensesInvoicedRequest"];

/**
 * Marks one approved, billable line as billed on to the customer. The same
 * financial rights the pricing door asks for, and the period lock does not
 * reach it either — invoicing is bookkeeping done after a period closes. A per
 * diem day is refused on `kind`, and `capabilities.canMarkInvoiced` is false
 * for one, so the button is never offered.
 */
export const markExpenseInvoiced = (id: number, input: InvoicedInput): Promise<Expense> =>
  request<Expense>(`/api/v1/expenses/entries/${id}/invoiced`, json("POST", input));

export type InvoicedUndoInput = Schemas["ExpensesInvoicedUndoRequest"];

/**
 * Takes the invoicing back off one line, under exactly the rights that set it
 * and with the same revision guard. The reference goes with it: what is
 * recorded is that the line went out on an invoice, and it did not.
 */
export const undoExpenseInvoiced = (id: number, input: InvoicedUndoInput): Promise<Expense> =>
  request<Expense>(`/api/v1/expenses/entries/${id}/invoiced/undo`, json("POST", input));
