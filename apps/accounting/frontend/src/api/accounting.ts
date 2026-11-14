import { queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { ACCOUNTING_QUERY_KEY, handleUnauthorized, json, queryString, refusalOf, request, send } from "./request";

type Schemas = components["schemas"];

export type AccountingMeta = Schemas["AccountingMeta"];
export type AccountingSettings = Schemas["AccountingSettings"];
export type AccountingAccount = Schemas["AccountingAccount"];
export type AccountingAccountKind = Schemas["AccountingAccountKind"];
export type AccountingSystemRole = Schemas["AccountingSystemRole"];
export type AccountingGrouping = Schemas["AccountingGrouping"];
export type AccountingGroupingCode = Schemas["AccountingGroupingCode"];
export type AccountingTaxCode = Schemas["AccountingTaxCode"];
export type AccountingVoucher = Schemas["AccountingVoucher"];
export type AccountingVoucherSummary = Schemas["AccountingVoucherSummary"];
export type AccountingVoucherList = Schemas["AccountingVoucherList"];
export type AccountingVoucherKind = Schemas["AccountingVoucherKind"];
export type AccountingJournalInput = Schemas["AccountingJournalInput"];
export type AccountingJournalPreview = Schemas["AccountingJournalPreview"];
export type AccountingAttachment = Schemas["AccountingAttachment"];
export type AccountingLock = Schemas["AccountingLock"];
export type AccountingOpening = Schemas["AccountingOpening"];
export type AccountingOpeningInput = Schemas["AccountingOpeningInput"];
export type AccountingOpenItem = Schemas["AccountingOpenItem"];
export type AccountingReport = Schemas["AccountingReport"];
export type AccountingReportKind = Schemas["AccountingReportKind"];
export type AccountingVatTerm = Schemas["AccountingVatTerm"];

const BASE = "/api/v1/accounting";

export const accountKinds: readonly AccountingAccountKind[] = ["asset", "liability", "equity", "income", "expense"];
export const systemRoles: readonly AccountingSystemRole[] = [
  "receivables",
  "payables",
  "bank",
  "vat_settlement",
  "rounding",
  "bad_debt",
  "employee_payables",
  "opening",
  "year_result",
  "clearing_vipps",
  "charge_income",
  "currency_gain",
  "currency_loss",
  "payment_fees",
];
export const voucherKinds: readonly AccountingVoucherKind[] = [
  "manual",
  "opening",
  "sales_invoice",
  "sales_credit_note",
  "customer_payment",
  "charge",
  "write_off",
  "supplier_invoice",
  "supplier_credit_note",
  "supplier_payment",
  "expense",
  "reimbursement",
  "vat_settlement",
  "bank",
  "import",
];
export const reportKinds: readonly AccountingReportKind[] = [
  "journal",
  "account",
  "customers",
  "suppliers",
  "trial_balance",
  "profit_and_loss",
  "balance_sheet",
];
export const vatTerms: readonly AccountingVatTerm[] = ["bimonthly", "yearly", "none"];

/** What the caller may do, and whether the books are set up (R12). */
export const metaQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "meta"],
    queryFn: ({ signal }) => request<AccountingMeta>(`${BASE}/meta`, { signal }),
    staleTime: 30_000,
  });

export const setUpBooks = (body: {
  booksStart: string;
  fiscalYearStartMonth: number;
  vatTerm: AccountingVatTerm;
  language: "en" | "nb";
}) => send<AccountingSettings>(`${BASE}/setup`, json("POST", body));

export const accountsQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "accounts"],
    queryFn: ({ signal }) => request<{ items: AccountingAccount[] }>(`${BASE}/accounts`, { signal }),
    select: (answer) => answer.items,
  });

export const createAccount = (body: {
  number: string;
  name: string;
  kind: AccountingAccountKind;
  defaultTaxCodeId?: number | null;
  grouping?: AccountingGrouping | null;
}) => send<AccountingAccount>(`${BASE}/accounts`, json("POST", body));

export const updateAccount = (
  id: number,
  body: {
    number: string;
    name: string;
    kind: AccountingAccountKind;
    defaultTaxCodeId: number | null;
    active: boolean;
    revision: number;
  },
) => send<AccountingAccount>(`${BASE}/accounts/${id}`, json("PUT", body));

export const deleteAccount = (id: number) => send<void>(`${BASE}/accounts/${id}`, { method: "DELETE" });

export const putGroupings = (id: number, groupings: AccountingGrouping[]) =>
  send<AccountingAccount>(`${BASE}/accounts/${id}/groupings`, json("PUT", { groupings }));

export const assignRole = (role: AccountingSystemRole, accountId: number) =>
  send<{ items: AccountingAccount[] }>(`${BASE}/roles/${role}`, json("PUT", { accountId }));

export const groupingCodesQueryOptions = (year: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "grouping-codes", year],
    queryFn: ({ signal }) =>
      request<{ year: number; items: AccountingGroupingCode[] }>(`${BASE}/grouping-codes${queryString({ year })}`, {
        signal,
      }),
    staleTime: Number.POSITIVE_INFINITY,
  });

export const taxCodesQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "tax-codes"],
    queryFn: ({ signal }) => request<{ items: AccountingTaxCode[] }>(`${BASE}/tax-codes`, { signal }),
    select: (answer) => answer.items,
  });

export const updateTaxCode = (
  id: number,
  body: {
    name: string;
    vatAccountId: number | null;
    counterVatAccountId: number | null;
    active: boolean;
    revision: number;
  },
) => send<AccountingTaxCode>(`${BASE}/tax-codes/${id}`, json("PUT", body));

export const addTaxRate = (id: number, body: { ratePercent: string; validFrom: string }) =>
  send<AccountingTaxCode>(`${BASE}/tax-codes/${id}/rates`, json("POST", body));

export interface VoucherFilters {
  from?: string;
  to?: string;
  kind?: AccountingVoucherKind;
  accountId?: number;
  customerId?: number;
  q?: string;
  offset?: number;
  limit?: number;
}

export const vouchersQueryOptions = (filters: VoucherFilters) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "vouchers", filters],
    queryFn: ({ signal }) =>
      request<AccountingVoucherList>(`${BASE}/vouchers${queryString({ ...filters })}`, { signal }),
  });

export const voucherQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "voucher", id],
    queryFn: ({ signal }) => request<AccountingVoucher>(`${BASE}/vouchers/${id}`, { signal }),
  });

export const postVoucher = (body: AccountingJournalInput) =>
  send<AccountingVoucher>(`${BASE}/vouchers`, json("POST", body));

export const previewVoucher = (body: AccountingJournalInput, signal?: AbortSignal) =>
  send<AccountingJournalPreview>(`${BASE}/vouchers/preview`, { ...json("POST", body), signal });

export const reverseVoucher = (id: number, body: { date: string; reason: string }) =>
  send<AccountingVoucher>(`${BASE}/vouchers/${id}/reverse`, json("POST", body));

/** Attaches a file to a voucher: multipart, the field `file`. */
export const uploadAttachment = async (voucherId: number, file: File): Promise<AccountingAttachment> => {
  const form = new FormData();
  form.append("file", file);
  const response = await fetch(appUrl(`${BASE}/vouchers/${voucherId}/attachments`), {
    method: "POST",
    body: form,
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (await response.json()) as AccountingAttachment;
  throw await refusalOf(response);
};

/** The address an attachment downloads from. */
export const attachmentUrl = (voucherId: number, attachmentId: number) =>
  appUrl(`${BASE}/vouchers/${voucherId}/attachments/${attachmentId}`);

export const lockQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "lock"],
    queryFn: ({ signal }) => request<AccountingLock>(`${BASE}/lock`, { signal }),
  });

export const putLock = (body: { lockedThrough: string | null; reason?: string | null }) =>
  send<AccountingLock>(`${BASE}/lock`, json("PUT", body));

export const openingQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "opening"],
    queryFn: ({ signal }) => request<AccountingOpening>(`${BASE}/opening`, { signal }),
  });

export const postOpening = (body: AccountingOpeningInput) =>
  send<AccountingVoucher>(`${BASE}/opening`, json("POST", body));

export const openItemsQueryOptions = (filters: {
  kind?: "customer" | "supplier";
  open?: boolean;
  offset?: number;
  limit?: number;
}) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "open-items", filters],
    queryFn: ({ signal }) =>
      request<{ items: AccountingOpenItem[]; total: number }>(`${BASE}/open-items${queryString({ ...filters })}`, {
        signal,
      }),
  });

export const settleOpenItem = (
  id: number,
  body: { date: string; amount: string; bankAccountId?: number | null; text?: string },
) => send<AccountingVoucher>(`${BASE}/open-items/${id}/settlements`, json("POST", body));

export interface ReportParams {
  from: string;
  to: string;
  compareFrom?: string;
  compareTo?: string;
  accountId?: number;
  projectId?: number;
}

export const reportQueryOptions = (kind: AccountingReportKind, params: ReportParams) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "report", kind, params],
    queryFn: ({ signal }) =>
      request<AccountingReport>(`${BASE}/reports/${kind}${queryString({ ...params })}`, { signal }),
  });

/** The address a report's CSV downloads from. */
export const reportCsvUrl = (kind: AccountingReportKind, params: ReportParams) =>
  appUrl(`${BASE}/reports/${kind}/csv${queryString({ ...params })}`);
