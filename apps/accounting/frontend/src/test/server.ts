import type {
  AccountingAccount,
  AccountingLock,
  AccountingMeta,
  AccountingOpening,
  AccountingPostingRule,
  AccountingReport,
  AccountingSupplier,
  AccountingSupplierInvoice,
  AccountingTaxCode,
  AccountingUnposted,
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
  unposted: 0,
  expensesEnabled: false,
  informational: 0,
  payablesOverdue: 0,
  payablesDueSoon: 0,
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
  account(1005, "3000", "Salgsinntekt, avgiftspliktig", { kind: "income" }),
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
  sourceDocumentId: null,
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
    taxSpecification: null,
    postingRuleId: null,
    postingRuleName: null,
    postingRuleVersion: null,
    currency: null,
    currencyAmount: null,
    exchangeRate: null,
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
      documentId: null,
      supplierId: null,
      supplierInvoiceId: null,
    },
    {
      style: "total",
      depth: 0,
      cells: { name: "Sum", closing: "0.00" },
      voucherId: null,
      accountId: null,
      documentId: null,
      supplierId: null,
      supplierInvoiceId: null,
    },
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
  rules: AccountingPostingRule[];
  unposted: AccountingUnposted[];
  /** The table every report answers; the trial balance's unless set. */
  report?: AccountingReport;
  suppliers: AccountingSupplier[];
  supplierInvoices: AccountingSupplierInvoice[];
}

export const supplier = (change: Partial<AccountingSupplier> = {}): AccountingSupplier => ({
  id: 3001,
  number: 1,
  name: "Rørlegger Hansen AS",
  organisationNumber: "923609016",
  country: "NO",
  addressLine: "Storgata 1",
  postalCode: "0155",
  city: "Oslo",
  email: null,
  bankAccount: "12345678903",
  iban: null,
  bic: null,
  paymentTermsDays: 30,
  defaultAccountId: 1003,
  defaultTaxCodeId: 2001,
  currency: "NOK",
  isPerson: false,
  active: true,
  hasPostings: false,
  balance: "0.00",
  lastVoucherDate: null,
  retainedUntil: null,
  revision: 1,
  createdAt: STAMP,
  updatedAt: STAMP,
  ...change,
});

export const supplierInvoice = (change: Partial<AccountingSupplierInvoice> = {}): AccountingSupplierInvoice => ({
  id: 4001,
  supplierId: 3001,
  supplierName: "Rørlegger Hansen AS",
  supplierNumber: 1,
  kind: "invoice",
  invoiceNumber: "F-1",
  invoiceDate: "2026-11-01",
  dueDate: "2026-12-01",
  currency: "NOK",
  exchangeRate: "1.000000",
  total: "1250.00",
  totalNok: "1250.00",
  linesTotal: "1250.00",
  vat: "250.00",
  kid: null,
  reference: null,
  description: "",
  state: "draft",
  balance: null,
  balanceCurrency: null,
  overdue: false,
  lines: [
    {
      id: 1,
      position: 1,
      accountId: 1003,
      accountNumber: "6300",
      accountName: "Leie av lokaler",
      taxCodeId: 2001,
      taxCode: "1",
      amount: "1250.00",
      entry: "gross",
      projectId: null,
      projectName: null,
      text: "",
      net: "1000.00",
      vat: "250.00",
      gross: "1250.00",
    },
  ],
  files: [],
  payments: [],
  voucher: null,
  reversalVoucher: null,
  openItemId: null,
  creditsInvoiceId: null,
  copiedFromId: null,
  sourceExpenseEntryId: null,
  revision: 1,
  createdAt: STAMP,
  updatedAt: STAMP,
  postedAt: null,
  ...change,
});

export const rule = (change: Partial<AccountingPostingRule> = {}): AccountingPostingRule => ({
  id: 1001,
  name: "Avgiftspliktig salg, alminnelig sats (kode 3)",
  scope: "sales",
  match: "default",
  lineKind: null,
  taxCategoryId: null,
  taxCode: "3",
  accountId: 1005,
  accountNumber: "3000",
  accountName: "Salgsinntekt, avgiftspliktig",
  version: 1,
  revision: 1,
  updatedAt: STAMP,
  expenseCategoryId: null,
  vatDeductible: null,
  ...change,
});

export const unposted = (change: Partial<AccountingUnposted> = {}): AccountingUnposted => ({
  id: 7001,
  stream: "invoices.documents",
  position: 101,
  source: { module: "invoices", kind: "invoice", id: 9001, part: "" },
  factDate: "2026-11-02",
  documentId: 9001,
  documentNumber: "101",
  customerId: 501,
  customerName: "Acme AS",
  amount: "1250.00",
  reason: "tax_code_unknown",
  detail: 'The standard tax code "99" is not an active tax code in the books.',
  attempts: 1,
  firstSeenAt: STAMP,
  lastTriedAt: STAMP,
  postedAt: null,
  postedVoucher: null,
  invoiceId: 9001,
  waitingOn: null,
  orderKey: null,
  informational: false,
  resolvedAt: null,
  supplierInvoiceId: null,
  expenseLines: [],
  ...change,
});

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
    rules: [
      rule(),
      rule({ id: 1002, name: "Timer", match: "line_kind", lineKind: "hours", taxCode: null, version: 2 }),
    ],
    unposted: [],
    suppliers: [supplier()],
    supplierInvoices: [],
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
    if (method === "GET" && path === "/vouchers") {
      const limit = Number(url.searchParams.get("limit") ?? state.vouchers.length);
      return jsonResponse(200, { items: state.vouchers.slice(0, limit), total: state.vouchers.length });
    }
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
    if (method === "GET" && path.startsWith("/reports/")) return jsonResponse(200, state.report ?? report());
    if (method === "GET" && path === "/posting-rules") return jsonResponse(200, { items: state.rules });
    if (method === "POST" && path === "/posting-rules")
      return jsonResponse(201, rule({ id: 1003, ...JSON.parse(String(init?.body)) }));
    const ruleId = /^\/posting-rules\/(\d+)$/.exec(path);
    if (method === "PUT" && ruleId) return jsonResponse(200, rule({ id: Number(ruleId[1]), version: 2 }));
    if (method === "DELETE" && ruleId) return new Response(null, { status: 204 });
    if (method === "GET" && path === "/unposted")
      return jsonResponse(200, { items: state.unposted, total: state.unposted.length });
    const retried = /^\/unposted\/(\d+)\/retry$/.exec(path);
    if (method === "POST" && retried)
      return jsonResponse(
        200,
        unposted({
          id: Number(retried[1]),
          postedAt: STAMP,
          postedVoucher: { id: 5009, display: "2026-000019" },
          attempts: 2,
        }),
      );
    if (method === "GET" && path === "/api/v1/expenses/categories")
      return jsonResponse(200, [
        { id: 1001, name: "Materials", active: true, position: 1 },
        { id: 1006, name: "Meals", active: true, position: 6 },
      ]);
    if (method === "GET" && path === "/suppliers")
      return jsonResponse(200, { items: state.suppliers, total: state.suppliers.length });
    if (method === "GET" && path === "/suppliers/lookup/brreg")
      return jsonResponse(200, {
        items: [
          {
            organisationNumber: "923609016",
            name: "EQUINOR ASA",
            addressLine: "Forusbeen 50",
            postalCode: "4035",
            city: "STAVANGER",
            country: "NO",
            vatRegistered: true,
            deleted: false,
          },
        ],
      });
    if (method === "POST" && path === "/suppliers")
      return jsonResponse(201, supplier({ id: 3002, number: 2, ...JSON.parse(String(init?.body)) }));
    const supplierId = /^\/suppliers\/(\d+)$/.exec(path);
    if (supplierId && method === "GET") {
      const found = state.suppliers.find((x) => x.id === Number(supplierId[1]));
      return found ? jsonResponse(200, found) : new Response(null, { status: 404 });
    }
    if (method === "GET" && path === "/supplier-invoices") {
      const number = url.searchParams.get("supplierNumber");
      const items = state.supplierInvoices.filter((i) => !number || i.invoiceNumber === number);
      return jsonResponse(200, { items, total: items.length });
    }
    if (method === "POST" && path === "/supplier-invoices")
      return jsonResponse(201, supplierInvoice({ id: 4009, ...JSON.parse(String(init?.body)) }));
    const invoiceId = /^\/supplier-invoices\/(\d+)(\/.*)?$/.exec(path);
    if (invoiceId) {
      const found = state.supplierInvoices.find((x) => x.id === Number(invoiceId[1])) ?? supplierInvoice();
      const rest = invoiceId[2] ?? "";
      if (method === "GET" && rest === "") return jsonResponse(200, found);
      if (method === "PUT" && rest === "") return jsonResponse(200, found);
      if (method === "POST" && rest === "/post")
        return jsonResponse(200, { ...found, state: "posted", voucher: { id: 5010, display: "2026-000020" } });
      if (method === "POST" && rest === "/payments")
        return jsonResponse(201, { ...found, state: "paid", balance: "0.00" });
      if (method === "DELETE" && rest.startsWith("/payments/")) return jsonResponse(200, found);
      if (method === "POST" && rest === "/reverse-and-copy")
        return jsonResponse(201, supplierInvoice({ id: 4010, copiedFromId: found.id }));
    }
    const nok = /^\/unposted\/(\d+)\/nok-amounts$/.exec(path);
    if (method === "POST" && nok)
      return jsonResponse(
        200,
        unposted({ id: Number(nok[1]), postedAt: STAMP, postedVoucher: { id: 5011, display: "2026-000021" } }),
      );
    if (method === "POST" && /^\/unposted\/\d+\/supplier-invoice$/.test(path))
      return jsonResponse(201, supplierInvoice({ id: 4011, sourceExpenseEntryId: 51 }));
    const ack = /^\/unposted\/(\d+)\/acknowledge$/.exec(path);
    if (method === "POST" && ack) return jsonResponse(200, unposted({ id: Number(ack[1]), resolvedAt: STAMP }));
    if (method === "GET" && path === "/catch-up")
      return jsonResponse(200, {
        unposted: state.unposted.length,
        streams: [
          {
            stream: "invoices.documents",
            position: 101,
            behind: 0,
            behindMore: false,
            lastRunAt: STAMP,
            requestedAt: null,
            lastError: null,
          },
          {
            stream: "invoices.facts",
            position: 42,
            behind: 3,
            behindMore: false,
            lastRunAt: STAMP,
            requestedAt: null,
            lastError: null,
          },
        ],
      });
    return new Response(null, { status: 404 });
  });
};

/** The body of a request the fake received. */
export const parseBody = (body: BodyInit | null | undefined): unknown =>
  typeof body === "string" ? JSON.parse(body) : undefined;
