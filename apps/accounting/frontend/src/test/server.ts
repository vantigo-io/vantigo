import type {
  AccountingAccount,
  AccountingLock,
  AccountingMeta,
  AccountingOpening,
  AccountingReport,
  AccountingTaxCode,
  AccountingVoucher,
} from "../api/accounting";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";

const STAMP = "2026-11-14T09:00:00Z";

export const meta = (change: Partial<AccountingMeta> = {}): AccountingMeta => ({
  setUp: true,
  canPost: true,
  canClose: true,
  canExport: true,
  currency: "NOK",
  today: "2026-11-14",
  settings: {
    booksStart: "2026-01-01",
    fiscalYearStartMonth: 1,
    vatTerm: "bimonthly",
    lockedThrough: "2026-08-31",
    chartLanguage: "nb",
    setUpAt: STAMP,
  },
  projectsEnabled: false,
  invoicesEnabled: true,
  attachmentsEnabled: true,
  vouchersThisYear: 12,
  openCustomerItems: 3,
  ...change,
});

export const account = (
  id: number,
  number: string,
  name: string,
  change: Partial<AccountingAccount> = {},
): AccountingAccount => ({
  id,
  number,
  name,
  kind: "expense",
  defaultTaxCodeId: null,
  active: true,
  systemRole: null,
  hasPostings: false,
  groupings: [{ fromYear: 2026, category: "annenDriftskostnad", code: number }],
  revision: 1,
  ...change,
});

export const accounts = (): AccountingAccount[] => [
  account(1001, "1500", "Kundefordringer", { kind: "asset", systemRole: "receivables" }),
  account(1002, "1920", "Bankinnskudd", { kind: "asset", systemRole: "bank" }),
  account(1003, "6300", "Leie av lokaler", { defaultTaxCodeId: 2001 }),
  account(1004, "2710", "Inngående merverdiavgift, alminnelig sats", { kind: "liability" }),
];

export const taxCodes = (): AccountingTaxCode[] => [
  {
    id: 2001,
    code: "1",
    name: "Fradragsberettiget innenlands inngående merverdiavgift, 25 %",
    direction: "input",
    reverseCharge: false,
    vatAccountId: 1004,
    counterVatAccountId: null,
    reported: true,
    active: true,
    currentRate: "25.00",
    rates: [{ ratePercent: "25.00", validFrom: "2026-01-01", validTo: null }],
    revision: 1,
  },
];

export const voucher = (change: Partial<AccountingVoucher> = {}): AccountingVoucher => ({
  id: 5001,
  fiscalYear: 2026,
  number: 7,
  display: "2026-000007",
  voucherDate: "2026-10-01",
  documentDate: "2026-10-01",
  description: "Husleie oktober",
  kind: "manual",
  amount: "1250.00",
  source: null,
  system: false,
  rule: null,
  ruleVersion: null,
  reverses: null,
  reversedBy: null,
  reversalReason: null,
  postedAt: STAMP,
  postedByUserId: "11111111-1111-1111-1111-111111111111",
  postings: [
    posting(1, 1003, "6300", "Leie av lokaler", "1000.00", { taxCodeId: 2001, taxCode: "1", taxRole: "base" }),
    posting(2, 1004, "2710", "Inngående merverdiavgift", "250.00", {
      taxCodeId: 2001,
      taxCode: "1",
      taxRole: "tax",
      taxBase: "1000.00",
    }),
    posting(3, 1002, "1920", "Bankinnskudd", "-1250.00"),
  ],
  attachments: [],
  ...change,
});

export function posting(
  lineNo: number,
  accountId: number,
  accountNumber: string,
  accountName: string,
  amount: string,
  change: Partial<AccountingVoucher["postings"][number]> = {},
): AccountingVoucher["postings"][number] {
  return {
    lineNo,
    accountId,
    accountNumber,
    accountName,
    amount,
    taxCodeId: null,
    taxCode: null,
    taxRole: null,
    taxBase: null,
    customerId: null,
    customerName: null,
    supplierId: null,
    openItemId: null,
    projectId: null,
    projectName: null,
    description: "",
    ...change,
  };
}

export const lock = (): AccountingLock => ({
  lockedThrough: "2026-08-31",
  booksStart: "2026-01-01",
  events: [
    {
      lockedFrom: null,
      lockedTo: "2026-08-31",
      reason: null,
      changedAt: STAMP,
      changedByUserId: "11111111-1111-1111-1111-111111111111",
    },
  ],
});

export const report = (): AccountingReport => ({
  kind: "trial_balance",
  title: "Saldobalanse",
  from: "2026-01-01",
  to: "2026-11-14",
  compareFrom: null,
  compareTo: null,
  producedAt: STAMP,
  columns: [
    { key: "account", label: "Konto", type: "text" },
    { key: "name", label: "Navn", type: "text" },
    { key: "closing", label: "Utgående saldo", type: "amount" },
  ],
  rows: [
    {
      style: "line",
      depth: 0,
      cells: { account: "1920", name: "Bankinnskudd", closing: "29450.00" },
      voucherId: null,
      accountId: 1002,
    },
    { style: "total", depth: 0, cells: { name: "Sum", closing: "0.00" }, voucherId: null, accountId: null },
  ],
});

export interface ApiState {
  meta: AccountingMeta;
  accounts: AccountingAccount[];
  taxCodes: AccountingTaxCode[];
  vouchers: AccountingVoucher[];
  opening: AccountingOpening;
  /** Whether an attachment's upload is refused, as an installation without storage refuses it. */
  attachmentFails?: boolean;
}

/**
 * A fake of the accounting API over the stubbed fetch: the reads answer the
 * state, the writes answer what the server would, and every request is
 * recorded for the test to read.
 */
export const stubAccountingApi = (change: Partial<ApiState> = {}): StubbedFetch => {
  const state: ApiState = {
    meta: meta(),
    accounts: accounts(),
    taxCodes: taxCodes(),
    vouchers: [voucher()],
    opening: {
      booksStart: "2026-01-01",
      voucher: null,
      canEnter: true,
      locked: false,
      invoicesAvailable: true,
      openInvoices: [],
    },
    ...change,
  };
  return stubFetch(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = init?.method ?? "GET";
    const path = url.pathname.replace("/api/v1/accounting", "");
    if (method === "GET" && path === "/meta") return jsonResponse(200, state.meta);
    if (method === "GET" && path === "/accounts") return jsonResponse(200, { items: state.accounts });
    if (method === "GET" && path === "/tax-codes") return jsonResponse(200, { items: state.taxCodes });
    if (method === "GET" && path === "/grouping-codes") return jsonResponse(200, { year: 2026, items: [] });
    if (method === "GET" && path === "/vouchers")
      return jsonResponse(200, { items: state.vouchers, total: state.vouchers.length });
    const one = /^\/vouchers\/(\d+)$/.exec(path);
    if (method === "GET" && one) {
      const found = state.vouchers.find((v) => v.id === Number(one[1]));
      return found ? jsonResponse(200, found) : new Response(null, { status: 404 });
    }
    if (method === "POST" && path === "/vouchers/preview") {
      const body = JSON.parse(String(init?.body));
      const lines = body.lines.map((l: { accountId: number; amount: string; taxCodeId: number | null }, i: number) => ({
        lineNo: i + 1,
        fromLine: i,
        accountId: l.accountId,
        amount: l.amount,
        taxCodeId: l.taxCodeId,
        taxRole: l.taxCodeId ? "base" : null,
        taxBase: null,
        ratePercent: l.taxCodeId ? "25.00" : null,
      }));
      return jsonResponse(200, { lines, debit: "1250.00", credit: "1250.00", difference: "0.00", balanced: true });
    }
    if (method === "POST" && path === "/vouchers")
      return jsonResponse(201, voucher({ id: 5002, display: "2026-000008" }));
    if (method === "POST" && /^\/vouchers\/\d+\/reverse$/.test(path))
      return jsonResponse(
        201,
        voucher({ id: 5003, display: "2026-000009", reverses: { id: 5001, display: "2026-000007" } }),
      );
    if (method === "POST" && path === "/setup") return jsonResponse(201, state.meta.settings);
    if (method === "GET" && path === "/lock") return jsonResponse(200, lock());
    if (method === "PUT" && path === "/lock")
      return jsonResponse(200, { ...lock(), lockedThrough: JSON.parse(String(init?.body)).lockedThrough });
    if (method === "GET" && path === "/opening") return jsonResponse(200, state.opening);
    if (method === "GET" && path === "/open-items") return jsonResponse(200, { items: [], total: 0 });
    if (method === "POST" && /^\/vouchers\/\d+\/attachments$/.test(path))
      return state.attachmentFails
        ? jsonResponse(503, { status: 503, code: "storage_not_configured", title: "File storage is not configured" })
        : jsonResponse(201, {
            id: 9001,
            fileName: "receipt.pdf",
            contentType: "application/pdf",
            sizeBytes: 10,
            sha256: "0".repeat(64),
            uploadedAt: STAMP,
          });
    if (method === "GET" && path.startsWith("/reports/")) return jsonResponse(200, report());
    return new Response(null, { status: 404 });
  });
};

/** The body of a request the fake received. */
export const parseBody = (body: BodyInit | null | undefined): unknown =>
  typeof body === "string" ? JSON.parse(body) : undefined;
