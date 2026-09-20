import { isIsoDate } from "./dates";
import { type ExpenseKind, type ExpenseStatus, isExpenseStatus, isStandaloneExpenseKind } from "./status";

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
  /**
   * The travel claims' own page. "My expenses" lists two kinds of unit from
   * two paged endpoints, so each section pages itself — see the page's own
   * note on why a single merged page would be a lie.
   */
  claimPage: number;
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
  kind: isStandaloneExpenseKind(search.kind) ? search.kind : undefined,
  reimbursed: optionalFlag(search.reimbursed),
  from: isIsoDate(search.from) ? search.from : undefined,
  to: isIsoDate(search.to) ? search.to : undefined,
  page: Math.max(1, Number(search.page) || 1),
  claimPage: Math.max(1, Number(search.claimPage) || 1),
});

/**
 * Approvals' URL state. `waiting` is the queue itself — the submitted
 * expenses the caller may approve — and `approved` is where an approval is
 * taken back, because an approved expense has left the queue and the control
 * has to live somewhere.
 */
export interface ApprovalsSearch {
  state: "waiting" | "approved";
  page: number;
}

export const validateApprovalsSearch = (search: Record<string, unknown>): ApprovalsSearch => ({
  state: search.state === "approved" ? "approved" : "waiting",
  page: Math.max(1, Number(search.page) || 1),
});

/**
 * The payroll list's URL state. `waiting` is what a run is made from and
 * `reimbursed` is what has already been paid, where an undo is reachable.
 */
export interface ReimbursementsSearch {
  state: "waiting" | "reimbursed";
  from?: string;
  to?: string;
  page: number;
}

export const validateReimbursementsSearch = (search: Record<string, unknown>): ReimbursementsSearch => ({
  state: search.state === "reimbursed" ? "reimbursed" : "waiting",
  from: isIsoDate(search.from) ? search.from : undefined,
  to: isIsoDate(search.to) ? search.to : undefined,
  page: Math.max(1, Number(search.page) || 1),
});
