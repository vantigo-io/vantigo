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
export type AccountingPostingRule = Schemas["AccountingPostingRule"];
export type AccountingRuleMatch = Schemas["AccountingRuleMatch"];
export type AccountingUnposted = Schemas["AccountingUnposted"];
export type AccountingUnpostedList = Schemas["AccountingUnpostedList"];
export type AccountingCatchUp = Schemas["AccountingCatchUp"];
export type AccountingSupplier = Schemas["AccountingSupplier"];
export type AccountingSupplierInput = Schemas["AccountingSupplierInput"];
export type AccountingSupplierList = Schemas["AccountingSupplierList"];
export type AccountingBrregEntity = Schemas["AccountingBrregEntity"];
export type AccountingSupplierExport = Schemas["AccountingSupplierExport"];
export type AccountingSupplierInvoice = Schemas["AccountingSupplierInvoice"];
export type AccountingSupplierInvoiceInput = Schemas["AccountingSupplierInvoiceInput"];
export type AccountingSupplierInvoiceLine = Schemas["AccountingSupplierInvoiceLine"];
export type AccountingSupplierInvoiceLineInput = Schemas["AccountingSupplierInvoiceLineInput"];
export type AccountingSupplierInvoiceList = Schemas["AccountingSupplierInvoiceList"];
export type AccountingSupplierInvoiceState = Schemas["AccountingSupplierInvoiceState"];
export type AccountingSupplierPayment = Schemas["AccountingSupplierPayment"];
export type AccountingEntryMode = Schemas["AccountingEntryMode"];
export type AccountingVatTermList = Schemas["AccountingVatTermList"];
export type AccountingVatTermItem = Schemas["AccountingVatTermItem"];
export type AccountingVatTermDetail = Schemas["AccountingVatTermDetail"];
export type AccountingVatReturn = Schemas["AccountingVatReturn"];
export type AccountingVatReturnLine = Schemas["AccountingVatReturnLine"];
export type AccountingVatSpecification = Schemas["AccountingVatSpecification"];
export type AccountingVatCodeFigures = Schemas["AccountingVatCodeFigures"];
export type AccountingVatPosting = Schemas["AccountingVatPosting"];
export type AccountingVatPostingList = Schemas["AccountingVatPostingList"];
export type AccountingVatPart = Schemas["AccountingVatPart"];
export type AccountingVatGroup = Schemas["AccountingVatGroup"];

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
  "vat",
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
  /** The system vouchers of a document in Invoices (A13). */
  sourceDocumentId?: number;
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

/** The line kinds a posting rule may match (A14, Invoices' snapshot of what a line was made from). */
export const lineKinds = [
  "hours",
  "expense",
  "milestone",
  "settlement",
  "order_line",
  "subscription_period",
  "deduction",
  "manual",
  "mixed",
] as const;

/** The posting rules, in precedence order (A14). */
export const postingRulesQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "posting-rules"],
    queryFn: ({ signal }) => request<{ items: AccountingPostingRule[] }>(`${BASE}/posting-rules`, { signal }),
    select: (answer) => answer.items,
  });

export const createPostingRule = (body: {
  name: string;
  scope: "sales" | "expense";
  match: AccountingRuleMatch;
  lineKind?: string;
  taxCategoryId?: number;
  taxCode?: string;
  expenseCategoryId?: number;
  vatDeductible?: boolean;
  accountId: number;
}) => send<AccountingPostingRule>(`${BASE}/posting-rules`, json("POST", body));

export const updatePostingRule = (
  id: number,
  body: { name: string; accountId: number; revision: number; vatDeductible?: boolean },
) => send<AccountingPostingRule>(`${BASE}/posting-rules/${id}`, json("PUT", body));

export const deletePostingRule = (id: number) => send<void>(`${BASE}/posting-rules/${id}`, { method: "DELETE" });

/** The facts under Needs attention (A15). */
export const unpostedQueryOptions = (filters: { open?: boolean; offset?: number; limit?: number }) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "unposted", filters],
    queryFn: ({ signal }) =>
      request<AccountingUnpostedList>(`${BASE}/unposted${queryString({ ...filters })}`, { signal }),
  });

/** Enters the NOK amounts of an expense in another currency and posts it (R2). */
export const enterNokAmounts = (id: number, amounts: { entryId: number; amountNok: string }[]) =>
  send<AccountingUnposted>(`${BASE}/unposted/${id}/nok-amounts`, json("POST", { amounts }));

/** Makes a draft supplier invoice from an expense of the supplier_invoice kind (R2). */
export const makeSupplierInvoiceFromUnposted = (id: number, supplierIsPerson: boolean) =>
  send<AccountingSupplierInvoice>(`${BASE}/unposted/${id}/supplier-invoice`, json("POST", { supplierIsPerson }));

/** Acknowledges an informational row (vat_not_deducted). */
export const acknowledgeUnposted = (id: number) =>
  send<AccountingUnposted>(`${BASE}/unposted/${id}/acknowledge`, { method: "POST" });

/** Reads a fact again through Invoices and posts it. */
export const retryUnposted = (id: number) =>
  send<AccountingUnposted>(`${BASE}/unposted/${id}/retry`, { method: "POST" });

/** Where the catch-up of sales stands (A12, R7). */
export const catchUpQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "catch-up"],
    queryFn: ({ signal }) => request<AccountingCatchUp>(`${BASE}/catch-up`, { signal }),
  });

/** The suppliers (A17), by number. */
export const suppliersQueryOptions = (
  filters: { q?: string; active?: boolean; offset?: number; limit?: number } = {},
) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "suppliers", filters],
    queryFn: ({ signal }) =>
      request<AccountingSupplierList>(`${BASE}/suppliers${queryString({ ...filters })}`, { signal }),
  });

export const supplierQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "supplier", id],
    queryFn: ({ signal }) => request<AccountingSupplier>(`${BASE}/suppliers/${id}`, { signal }),
  });

export const createSupplier = (body: AccountingSupplierInput) =>
  send<AccountingSupplier>(`${BASE}/suppliers`, json("POST", body));

export const updateSupplier = (id: number, body: AccountingSupplierInput) =>
  send<AccountingSupplier>(`${BASE}/suppliers/${id}`, json("PUT", body));

export const deleteSupplier = (id: number) => send<void>(`${BASE}/suppliers/${id}`, { method: "DELETE" });

/** Anonymises a person supplier once the bookkeeping no longer keeps it (A17). */
export const anonymiseSupplier = (id: number) =>
  send<AccountingSupplier>(`${BASE}/suppliers/${id}/anonymise`, { method: "POST" });

/** What the books hold about a supplier, for a person's access request. */
export const exportSupplier = (id: number) => request<AccountingSupplierExport>(`${BASE}/suppliers/${id}/export`);

/** Looks a supplier up in Brønnøysundregisteret: by organisation number, or by name. */
export const lookupBrreg = (params: { organisationNumber?: string; q?: string }, signal?: AbortSignal) =>
  request<{ items: AccountingBrregEntity[] }>(`${BASE}/suppliers/lookup/brreg${queryString({ ...params })}`, {
    signal,
  });

export interface SupplierInvoiceFilters {
  state?: AccountingSupplierInvoiceState;
  supplierId?: number;
  supplierNumber?: string;
  dueFrom?: string;
  dueTo?: string;
  overdue?: boolean;
  offset?: number;
  limit?: number;
}

/** The supplier invoices (A18), newest first. */
export const supplierInvoicesQueryOptions = (filters: SupplierInvoiceFilters) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "supplier-invoices", filters],
    queryFn: ({ signal }) =>
      request<AccountingSupplierInvoiceList>(`${BASE}/supplier-invoices${queryString({ ...filters })}`, { signal }),
  });

export const supplierInvoiceQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "supplier-invoice", id],
    queryFn: ({ signal }) => request<AccountingSupplierInvoice>(`${BASE}/supplier-invoices/${id}`, { signal }),
  });

export const createSupplierInvoice = (body: AccountingSupplierInvoiceInput) =>
  send<AccountingSupplierInvoice>(`${BASE}/supplier-invoices`, json("POST", body));

export const updateSupplierInvoice = (id: number, body: AccountingSupplierInvoiceInput) =>
  send<AccountingSupplierInvoice>(`${BASE}/supplier-invoices/${id}`, json("PUT", body));

export const deleteSupplierInvoice = (id: number) =>
  send<void>(`${BASE}/supplier-invoices/${id}`, { method: "DELETE" });

export const postSupplierInvoice = (id: number) =>
  send<AccountingSupplierInvoice>(`${BASE}/supplier-invoices/${id}/post`, { method: "POST" });

export const reverseAndCopySupplierInvoice = (id: number, body: { date: string; reason: string }) =>
  send<AccountingSupplierInvoice>(`${BASE}/supplier-invoices/${id}/reverse-and-copy`, json("POST", body));

export const paySupplierInvoice = (
  id: number,
  body: { date: string; amountNok: string; amountCurrency?: string | null; bankAccountId?: number | null },
) => send<AccountingSupplierInvoice>(`${BASE}/supplier-invoices/${id}/payments`, json("POST", body));

export const removeSupplierPayment = (id: number, paymentId: number) =>
  send<AccountingSupplierInvoice>(`${BASE}/supplier-invoices/${id}/payments/${paymentId}`, { method: "DELETE" });

/** Attaches a file to a draft supplier invoice: multipart, the field `file`. */
export const uploadSupplierInvoiceFile = async (id: number, file: File): Promise<AccountingAttachment> => {
  const form = new FormData();
  form.append("file", file);
  const response = await fetch(appUrl(`${BASE}/supplier-invoices/${id}/files`), {
    method: "POST",
    body: form,
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (await response.json()) as AccountingAttachment;
  throw await refusalOf(response);
};

export const deleteSupplierInvoiceFile = (id: number, fileId: number) =>
  send<void>(`${BASE}/supplier-invoices/${id}/files/${fileId}`, { method: "DELETE" });

/** The address a supplier invoice's file downloads from. */
export const supplierInvoiceFileUrl = (id: number, fileId: number) =>
  appUrl(`${BASE}/supplier-invoices/${id}/files/${fileId}`);

/** The VAT terms (A22): from the books' first through today's, each with its state, due date and total. */
export const vatTermsQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "vat", "terms"],
    queryFn: ({ signal }) => request<AccountingVatTermList>(`${BASE}/vat/terms`, { signal }),
  });

/** A term's page: the return, the specification with the reconciliation, the close's checks and its history. */
export const vatTermQueryOptions = (year: number, number: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "vat", "term", year, number],
    queryFn: ({ signal }) => request<AccountingVatTermDetail>(`${BASE}/vat/terms/${year}/${number}`, { signal }),
  });

const termUrl = (year: number, number: number) => `${BASE}/vat/terms/${year}/${number}`;

export const closeVatTerm = (year: number, number: number, confirmPartial = false) =>
  send<AccountingVatTermDetail>(
    `${termUrl(year, number)}/close`,
    confirmPartial ? json("POST", { confirmPartial: true }) : { method: "POST" },
  );

export const reopenVatTerm = (year: number, number: number, reason: string) =>
  send<AccountingVatTermDetail>(`${termUrl(year, number)}/reopen`, json("POST", { reason }));

export const fileVatTerm = (year: number, number: number, body: { filedAt: string; reference: string }) =>
  send<AccountingVatTermDetail>(`${termUrl(year, number)}/filing`, json("POST", body));

export const unfileVatTerm = (year: number, number: number, reason: string) =>
  send<AccountingVatTermDetail>(`${termUrl(year, number)}/unfile`, json("POST", { reason }));

/** The address a closed term's return downloads from, as XML. */
export const vatReturnXmlUrl = (year: number, number: number) => appUrl(`${termUrl(year, number)}/xml`);

/** The address one of a term's closings downloads from, as XML: every close is kept (H2). */
export const vatClosingXmlUrl = (year: number, number: number, closing: number) =>
  appUrl(`${termUrl(year, number)}/closings/${closing}/xml`);

/** What the drill-down behind a VAT figure is asked for (A21). */
export interface VatPostingFilters {
  from: string;
  to: string;
  taxCodeId?: number;
  part?: AccountingVatPart;
  unreconciled?: boolean;
  offset?: number;
  limit?: number;
}

export const vatPostingsQueryOptions = (filters: VatPostingFilters) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "vat", "postings", filters],
    queryFn: ({ signal }) =>
      request<AccountingVatPostingList>(`${BASE}/vat/postings${queryString({ ...filters })}`, { signal }),
  });

export type AccountingSaftExport = Schemas["AccountingSaftExport"];
export type AccountingSaftExportCheck = Schemas["AccountingSaftExportCheck"];
export type AccountingSaftExportCreated = Schemas["AccountingSaftExportCreated"];
export type AccountingSaftExportList = Schemas["AccountingSaftExportList"];
export type AccountingSaftImport = Schemas["AccountingSaftImport"];
export type AccountingSaftImportApply = Schemas["AccountingSaftImportApply"];
export type AccountingSaftImportList = Schemas["AccountingSaftImportList"];

/** The SAF-T exports (A24), newest first; refetched while one is queued or running. */
export const saftExportsQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "saft", "exports"],
    queryFn: ({ signal }) => request<AccountingSaftExportList>(`${BASE}/saft-exports`, { signal }),
    select: (answer) => answer.items,
    refetchInterval: (query) =>
      query.state.data?.items.some((e) => e.state === "queued" || e.state === "running") ? 3000 : false,
  });

/** What an export of the selection would meet: the accounts with no grouping code, the company's details. */
export const saftExportCheckQueryOptions = (fiscalYear: number, fromPeriod: number, toPeriod: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "saft", "check", fiscalYear, fromPeriod, toPeriod],
    queryFn: ({ signal }) =>
      request<AccountingSaftExportCheck>(
        `${BASE}/saft-exports/check${queryString({ fiscalYear, fromPeriod, toPeriod })}`,
        {
          signal,
        },
      ),
  });

export const createSaftExport = (body: { fiscalYear: number; fromPeriod: number; toPeriod: number }) =>
  send<AccountingSaftExportCreated>(`${BASE}/saft-exports`, json("POST", body));

/** The address an export's file downloads from. */
export const saftExportFileUrl = (id: number, part: number) => appUrl(`${BASE}/saft-exports/${id}/files/${part}`);

/** The SAF-T files uploaded to set up the books from (A25). */
export const saftImportsQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "saft", "imports"],
    queryFn: ({ signal }) => request<AccountingSaftImportList>(`${BASE}/saft-imports`, { signal }),
    select: (answer) => answer.items,
  });

/** An import's preview, its customers matched again through Customers. */
export const saftImportQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "saft", "import", id],
    queryFn: ({ signal }) => request<AccountingSaftImport>(`${BASE}/saft-imports/${id}`, { signal }),
  });

/** Uploads a SAF-T file: multipart, the field `file`; answers its preview. */
export const uploadSaftImport = async (file: File): Promise<AccountingSaftImport> => {
  const form = new FormData();
  form.append("file", file);
  const response = await fetch(appUrl(`${BASE}/saft-imports`), { method: "POST", body: form, credentials: "include" });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (await response.json()) as AccountingSaftImport;
  throw await refusalOf(response);
};

export const applySaftImport = (id: number, body: AccountingSaftImportApply) =>
  send<AccountingSaftImport>(`${BASE}/saft-imports/${id}/apply`, json("POST", body));

/** The address an imported file downloads from, as it was uploaded. */
export const saftImportFileUrl = (id: number) => appUrl(`${BASE}/saft-imports/${id}/file`);
