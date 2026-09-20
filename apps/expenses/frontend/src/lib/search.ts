import { isIsoDate } from "./dates";
import { type ExpenseKind, type ExpenseStatus, isExpenseKind, isExpenseStatus } from "./status";

/**
 * My expenses' URL search params. Every filter lives in the URL so the list
 * survives a refresh and travels in a pasted link; the page reads them with
 * `useSearch` and writes them with `navigate`.
 */
export interface MyExpensesSearch {
  status?: ExpenseStatus;
  kind?: ExpenseKind;
  /** Whether the money has been paid back. Left out, both are in the list. */
  reimbursed?: boolean;
  from?: string;
  to?: string;
  page: number;
}

const optionalFlag = (value: unknown): boolean | undefined => {
  if (value === true || value === "true") return true;
  if (value === false || value === "false") return false;
  return undefined;
};

/**
 * The host route's `validateSearch` for My expenses (Task 8), exported here
 * so the route and this package's own tests validate the URL the same way.
 * Anything a hand-edited link invents falls back to "no filter" rather than
 * reaching the API as a value it would refuse with a 400.
 */
export const validateMyExpensesSearch = (search: Record<string, unknown>): MyExpensesSearch => ({
  status: isExpenseStatus(search.status) ? search.status : undefined,
  kind: isExpenseKind(search.kind) ? search.kind : undefined,
  reimbursed: optionalFlag(search.reimbursed),
  from: isIsoDate(search.from) ? search.from : undefined,
  to: isIsoDate(search.to) ? search.to : undefined,
  page: Math.max(1, Number(search.page) || 1),
});
