import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { PerDiemType } from "../lib/per-diem";
import type { ExpenseStatus } from "../lib/status";
import type { Expense, PaginatedResponse } from "./entries";
import { EXPENSES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * One travel claim with its lines — the unit a trip is. Its owner, status,
 * decision, reimbursement and period-lock judgement are the claim's; a line
 * keeps its own kind, date, amounts and receipts and carries no submit,
 * approve or reimburse of its own.
 *
 * The contract declares `status` as a plain string (OpenAPI 3.0 without enum
 * members), so it is narrowed here to the four values the server answers.
 */
export type Claim = Omit<Schemas["ExpensesClaimResponse"], "status" | "lines"> & {
  status: ExpenseStatus;
  lines: Expense[];
};

/** One travel claim in a list: the same header with a line count instead of the lines. */
export type ClaimListItem = Omit<Schemas["ExpensesClaimListResponse"], "status"> & { status: ExpenseStatus };

/**
 * One travel claim as a *unit* in a queue — the approval queue and the payroll
 * list. The trip at a glance, with the figures whoever is deciding needs; its
 * lines are one read away at `GET /claims/{id}`.
 *
 * It carries the trip's own `status` and, once a payroll run has paid it, the
 * same `reimbursement` stamp the claim itself carries — so a queue row says
 * what a trip is rather than inferring it from the list it arrived in. As on
 * `ClaimListItem`, the status is narrowed here to the four the server answers.
 */
export type ClaimSummary = Omit<Schemas["ExpensesClaimSummary"], "status"> & { status: ExpenseStatus };

export type ClaimCapabilities = Schemas["ExpensesClaimCapabilities"];
export type ClaimInput = Schemas["ExpensesClaimRequest"];

/** A replace carries the revision the claim was read at; a stale one is a 409. */
export type ClaimUpdateInput = Schemas["ExpensesClaimUpdateRequest"];

/**
 * One day the server proposes for a trip. It is a suggestion and nothing
 * more: nothing is written, and the client records whichever days the
 * traveller agrees with as ordinary per diem lines. `dayRate`, `amount` and
 * the `currency` the two are in are absent together when the table prices no
 * such day, and `exists` says the claim already holds a per diem line on that
 * date. The currency is the server's — the claim's own abroad one, or the
 * installation's default — so no client infers it from the claim.
 */
export type PerDiemSuggestedDay = Omit<Schemas["ExpensesPerDiemSuggestedDay"], "perDiemType"> & {
  perDiemType: PerDiemType;
};

/**
 * What `GET /expenses/claims` may be narrowed by. An absent filter is no
 * filter at all — and the list is never wider than what the caller may see,
 * so `userId` only ever narrows it. `from` and `to` are judged on the
 * departure **day in the installation's time zone**.
 */
export interface ClaimFilters {
  userId?: string;
  status?: ExpenseStatus;
  from?: string;
  to?: string;
  reimbursed?: boolean;
  page?: number;
  pageSize?: number;
}

const listQuery = (filters: ClaimFilters): string => {
  const query = new URLSearchParams();
  if (filters.userId) query.set("userId", filters.userId);
  if (filters.status) query.set("status", filters.status);
  if (filters.from) query.set("from", filters.from);
  if (filters.to) query.set("to", filters.to);
  if (filters.reimbursed !== undefined) query.set("reimbursed", String(filters.reimbursed));
  if (filters.page !== undefined) query.set("page", String(filters.page));
  if (filters.pageSize !== undefined) query.set("pageSize", String(filters.pageSize));
  const search = query.toString();
  return search ? `?${search}` : "";
};

/** One page of the travel claims the caller may see, the most recent trip first. */
export const expenseClaimsQueryOptions = (filters: ClaimFilters) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "claims", "list", filters],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<ClaimListItem>>(`/api/v1/expenses/claims${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

/** One travel claim with its lines. A claim the caller may not see is a bare 404. */
export const expenseClaimQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "claims", "detail", id],
    queryFn: ({ signal }) => request<Claim>(`/api/v1/expenses/claims/${id}`, { signal }),
  });

export const createClaim = (input: ClaimInput): Promise<Claim> =>
  request<Claim>("/api/v1/expenses/claims", json("POST", input));

/**
 * A full replace of the header, guarded by the revision the form was opened
 * at. What is left out is cleared. Narrowing the trip past a per diem day it
 * already holds is a 400 on `departureAt` or `returnAt` naming the stranded
 * dates; changing `abroad`, the day rate or the currency **reprices** every
 * per diem day in the same transaction, so the claim is read again afterwards.
 */
export const updateClaim = (id: number, input: ClaimUpdateInput): Promise<Claim> =>
  request<Claim>(`/api/v1/expenses/claims/${id}`, json("PUT", input));

/** Deletes the claim, its lines and their receipts. */
export const deleteClaim = (id: number): Promise<void> =>
  request<void>(`/api/v1/expenses/claims/${id}`, { method: "DELETE" });

/**
 * The days the server proposes for the trip. It writes nothing: whether the
 * traveller slept away is the one fact the stored times cannot tell, so it is
 * asked, and the answer decides whether the trip is counted in 24-hour
 * periods from the departure or as a single day.
 *
 * `overnight` is **always sent**, whatever the schema says. The contract
 * leaves it optional so that the server's own 400 is what names it rather
 * than a client's validator — a body without it is refused, not defaulted,
 * because a suggestion that guessed would answer a confident day count from
 * nothing.
 */
export const perDiemSuggestion = (claimId: number, overnight: boolean): Promise<PerDiemSuggestedDay[]> =>
  request<PerDiemSuggestedDay[]>(`/api/v1/expenses/claims/${claimId}/per-diem-suggestion`, json("POST", { overnight }));
