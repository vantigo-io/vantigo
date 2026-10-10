import { queryOptions } from "@tanstack/react-query";
import type { components, operations } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/** One imported bank file, its lines counted by status (invoices payments and reminders design D3). */
export type BankFile = Schemas["InvoicesBankFile"];
export type BankFileList = Schemas["PaginatedResponseOfInvoicesBankFile"];
/** GET /bank-files/{id}: the file and every line it brought, duplicates included. */
export type BankFileDetail = Schemas["InvoicesBankFileDetail"];
/** The upload's 201 and "Match the rest"'s 200: the file and what became of its lines. */
export type BankImportResult = Schemas["InvoicesBankImportResult"];
/** A file's ignored lines by kind. */
export type BankIgnored = Schemas["InvoicesBankIgnored"];
/** One receiving account a bank file was imported for, with its format and cutover. */
export type BankAccount = Schemas["InvoicesBankAccount"];
/** One line of a bank file and its state in matching and the exception queue (D4, D5). */
export type BankTransaction = Schemas["InvoicesBankTransaction"];
export type BankTransactionList = Schemas["PaginatedResponseOfInvoicesBankTransaction"];
export type BankTransactionEvent = Schemas["InvoicesBankTransactionEvent"];
export type BankTransactionApplied = Schemas["InvoicesBankTransactionApplied"];
export type BankTransactionSuggestion = Schemas["InvoicesBankTransactionSuggestion"];
export type BankTransactionTwin = Schemas["InvoicesBankTransactionTwin"];
export type BankTransactionReason = Schemas["InvoicesBankTransactionReason"];
export type BankTransactionStatus = BankTransaction["status"];
/** One part of an apply: an invoice, what it pays of the principal and of the charges. */
export type Allocation = Schemas["InvoicesAllocation"];
export type ApplyInput = Schemas["InvoicesBankTransactionApplyRequest"];
export type ReversalInput = Schemas["InvoicesBankTransactionReversalRequest"];
/** The queue's filters, as GET /bank-transactions takes them; paging is page/pageSize. */
export type BankTransactionFilters = NonNullable<operations["getInvoicesBankTransactions"]["parameters"]["query"]>;

/** The two formats a bank file comes in, in the order the screens offer them. */
export const BANK_FORMATS = ["ocr", "camt054"] as const;

/** The reasons a line is queued for, in the order the contract lists them. */
export const BANK_REASONS: readonly BankTransactionReason[] = [
  "kid_invalid",
  "kid_unknown",
  "invoice_credited",
  "invoice_settled",
  "exceeds_open",
  "no_kid",
  "negative_amount",
  "reversal",
  "vipps_payout",
  "paid_before_issue",
  "account_mismatch",
  "possible_duplicate",
  "payment_removed",
];

/** A line's statuses, the open ones first. */
export const BANK_STATUSES: readonly BankTransactionStatus[] = [
  "exception",
  "duplicate",
  "pending",
  "matched",
  "resolved",
];

const query = (filters: Record<string, string | number | boolean | undefined>): string => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const text = params.toString();
  return text ? `?${text}` : "";
};

export const bankFilesQueryOptions = (page: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-files", page],
    queryFn: ({ signal }) => request<BankFileList>(`/api/v1/invoices/bank-files${query({ page })}`, { signal }),
  });

/** How many pages of 100 a screen reads before it says it stopped: 500 lines or files. */
export const MAX_PAGES = 5;

/** Every page of a paged read, 100 at a time, at most MAX_PAGES; `truncated` when more were left. */
const allPages = async <T>(
  read: (page: number) => Promise<{ data: T[]; pagination: { hasNextPage: boolean } }>,
): Promise<{ data: T[]; truncated: boolean }> => {
  const data: T[] = [];
  for (let page = 1; page <= MAX_PAGES; page++) {
    const answer = await read(page);
    data.push(...answer.data);
    if (!answer.pagination.hasNextPage) return { data, truncated: false };
  }
  return { data, truncated: true };
};

/** The files the queue's File filter offers: up to MAX_PAGES pages of 100, newest first. */
export const bankFileChoicesQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-files", "choices"],
    queryFn: ({ signal }) =>
      allPages((page) =>
        request<BankFileList>(`/api/v1/invoices/bank-files${query({ page, pageSize: 100 })}`, { signal }),
      ),
  });

/**
 * The bank lines a reversal may have taken a payment back from: the matched
 * and the resolved lines of its account and amount, booked on or before it —
 * read whole, up to MAX_PAGES pages of 100 per status, `truncated` when more
 * were left.
 */
export const reversalLinesQueryOptions = (reversal: BankTransaction) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-transactions", "reversal", reversal.id],
    queryFn: async ({ signal }) => {
      const read = (status: BankTransactionStatus) =>
        allPages((page) =>
          request<BankTransactionList>(
            `/api/v1/invoices/bank-transactions${query({
              status,
              account: reversal.account,
              amount: Math.abs(reversal.amount),
              to: reversal.bookedOn,
              page,
              pageSize: 100,
            })}`,
            { signal },
          ),
        );
      const [matched, resolved] = await Promise.all([read("matched"), read("resolved")]);
      return { data: [...matched.data, ...resolved.data], truncated: matched.truncated || resolved.truncated };
    },
  });

export const bankFileQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-file", id],
    queryFn: ({ signal }) => request<BankFileDetail>(`/api/v1/invoices/bank-files/${id}`, { signal }),
  });

export const bankAccountsQueryOptions = () =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-accounts"],
    queryFn: ({ signal }) =>
      request<Schemas["InvoicesBankAccountsResponse"]>("/api/v1/invoices/bank-accounts", { signal }),
  });

export const bankTransactionsQueryOptions = (filters: BankTransactionFilters) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "bank-transactions", filters],
    queryFn: ({ signal }) =>
      request<BankTransactionList>(`/api/v1/invoices/bank-transactions${query(filters)}`, { signal }),
  });

/**
 * Uploads one bank file as the one multipart part named `file`; the browser
 * writes the multipart boundary, so no Content-Type is set here. A file its
 * own rules refuse is an `ApiValidationError` on `file`; the 409s and the
 * 503 carry their code.
 */
export const uploadBankFile = (file: File): Promise<BankImportResult> => {
  const form = new FormData();
  form.append("file", file, file.name);
  return request<BankImportResult>("/api/v1/invoices/bank-files", { method: "POST", body: form });
};

/** Matches a file's pending lines — "Match the rest" — the caller registering the payments. */
export const matchRest = (bankFileId: number): Promise<BankImportResult> =>
  request<BankImportResult>(`/api/v1/invoices/bank-files/${bankFileId}/match`, { method: "POST" });

/** Changes the format an account's files come in (`invoices:manage`); another format records the cutover. */
export const setAccountFormat = (account: string, format: string): Promise<BankAccount> =>
  request<BankAccount>(`/api/v1/invoices/bank-accounts/${account}/format`, json("PUT", { format }));

const action = (id: number, name: string, body?: unknown): Promise<BankTransaction> =>
  request<BankTransaction>(
    `/api/v1/invoices/bank-transactions/${id}/${name}`,
    body === undefined ? { method: "POST" } : json("POST", body),
  );

/** Applies an exception to one or more invoices and their charges (D5). */
export const applyTransaction = (id: number, input: ApplyInput): Promise<BankTransaction> => action(id, "apply", input);

/** Dismisses an exception as not a customer payment, with a note. */
export const dismissTransaction = (id: number, note: string): Promise<BankTransaction> =>
  action(id, "dismiss", { note });

/** Handles a reversal: the payments it takes back, or none with a note. */
export const handleReversal = (id: number, input: ReversalInput): Promise<BankTransaction> =>
  action(id, "handle-reversal", input);

/** Confirms a duplicate row, or a possible duplicate, with an optional note. */
export const confirmDuplicate = (id: number, note: string): Promise<BankTransaction> =>
  action(id, "confirm-duplicate", note ? { note } : {});

/** Keeps a duplicate row as a payment of its own: it becomes an exception queued possible_duplicate. */
export const treatAsDistinct = (id: number): Promise<BankTransaction> => action(id, "treat-as-distinct");

/** Returns a resolved line, or a matched one whose payments were all removed, to the queue. */
export const reopenTransaction = (id: number): Promise<BankTransaction> => action(id, "reopen");
