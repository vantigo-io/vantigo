import type {
  AccountingAccount,
  AccountingBankAccount,
  AccountingBankLine,
  AccountingBankReconciliation,
  AccountingBankRule,
  AccountingLock,
  AccountingMeta,
  AccountingOpening,
  AccountingPostingRule,
  AccountingReport,
  AccountingSaftExport,
  AccountingSaftImport,
  AccountingSupplier,
  AccountingSupplierInvoice,
  AccountingTaxCode,
  AccountingUnposted,
  AccountingVatTermDetail,
  AccountingVatTermItem,
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
  vatNextTerm: null,
  vatRegistrationMismatch: false,
  vatTermsToClose: 0,
  vatTermsToFile: 0,
  openBankLines: 0,
  bankFeedProblems: 0,
  bankFeedEnabled: false,
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
      taxCodeId: null,
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
      taxCodeId: null,
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
  vatTerms?: AccountingVatTermItem[];
  /** The page every term answers; vatTermDetail()'s unless set. */
  vatTerm?: AccountingVatTermDetail;
  /** Refusals by "METHOD /path": the problem's status and code the request is answered with. */
  refusals?: Record<string, { status: number; code: string }>;
  bankAccounts?: AccountingBankAccount[];
  bankLines?: AccountingBankLine[];
  bankRules?: AccountingBankRule[];
  /** The seller's account offered while no bank account has it. */
  bankSuggested?: boolean;
  /** Whether a lock moved forward is refused bank_unreconciled until confirmed. */
  bankUnreconciled?: boolean;
}

export const bankAccount = (change: Partial<AccountingBankAccount> = {}): AccountingBankAccount => ({
  id: 7001,
  name: "Driftskonto",
  accountNumber: "15030012345",
  iban: null,
  bic: null,
  currency: "NOK",
  ledgerAccountId: 1002,
  ledgerAccountNumber: "1920",
  ledgerAccountName: "Bankinnskudd",
  source: "file",
  feedStartDay: null,
  feed: null,
  csvMapping: null,
  active: true,
  openLines: 2,
  lastStatementTo: "2026-11-07",
  revision: 1,
  ...change,
});

export const bankLine = (change: Partial<AccountingBankLine> = {}): AccountingBankLine => ({
  id: 8101,
  statementId: 9001,
  bankAccountId: 7001,
  bookedOn: "2026-11-04",
  valueOn: null,
  amount: "-95.00",
  text: "Gebyr nettbank",
  reference: "",
  kid: null,
  counterpartyName: "",
  counterpartyAccount: "",
  bankId: "DNB-1",
  kind: "transfer",
  reversal: false,
  source: "camt053",
  batch: [],
  state: "open",
  match: null,
  suggestions: [
    {
      key: "rule:1:1",
      kind: "rule",
      certainty: "likely",
      exact: false,
      reason: "The bank rule Bankgebyr: to account 7770.",
      postings: [],
      lines: [
        {
          accountId: 1002,
          accountNumber: "1920",
          accountName: "Bankinnskudd",
          amount: "-95.00",
          taxCodeId: null,
          taxCode: null,
        },
        {
          accountId: 1006,
          accountNumber: "7770",
          accountName: "Bankgebyrer",
          amount: "95.00",
          taxCodeId: null,
          taxCode: null,
        },
      ],
      supplierInvoiceId: null,
      otherLineId: null,
      bankRuleId: 1,
      currency: null,
      amountCurrency: null,
      needsAmountCurrency: false,
      fee: null,
      feeMax: null,
    },
  ],
  customerPayment: false,
  possibleDuplicateOf: null,
  note: "",
  txReference: "",
  ...change,
});

const reconciliation = (state: ApiState): AccountingBankReconciliation => ({
  bankAccount: (state.bankAccounts ?? [bankAccount()])[0],
  from: "2026-11-01",
  to: "2026-11-30",
  lines: state.bankLines ?? [],
  total: (state.bankLines ?? []).length,
  foot: {
    statementBalance: "88655.00",
    ledgerBalance: "88750.00",
    difference: "-95.00",
    unmatchedPostings: [],
    unmatchedPostingsTotal: 0,
  },
});

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
  anonymisedAt: null,
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

export const vatTermItem = (change: Partial<AccountingVatTermItem> = {}): AccountingVatTermItem => ({
  year: 2026,
  number: 4,
  periodStart: "2026-07-01",
  periodEnd: "2026-08-31",
  periodName: "juli-august",
  dueDate: "2026-10-10",
  state: "open",
  total: "12345",
  closedAt: null,
  closedByUserId: null,
  settlementVoucher: null,
  filedAt: null,
  filedReference: null,
  filedByUserId: null,
  lockCovers: true,
  needsClosing: true,
  needsFiling: true,
  closing: null,
  filedClosing: null,
  partial: false,
  coversFrom: "2026-07-01",
  ...change,
});

export const vatTermDetail = (change: Partial<AccountingVatTermDetail> = {}): AccountingVatTermDetail => ({
  term: vatTermItem(),
  return: {
    year: 2026,
    periodName: "juli-august",
    periodStart: "2026-07-01",
    periodEnd: "2026-08-31",
    total: "12345",
    exactTotal: "12345.67",
    roundingDifference: "0.67",
    direction: "pay",
    organisationNumber: "923609016",
    warnings: [],
    lines: [
      {
        group: "sales_domestic",
        code: "3",
        name: "Utgående merverdiavgift, 25 %",
        side: "output",
        taxCodeId: 2003,
        base: "60000",
        exactBase: "60000.40",
        rate: "25",
        vat: "15000",
        exactVat: "15000.10",
        specification: null,
        remark: null,
      },
      {
        group: "purchases_domestic",
        code: "1",
        name: "Fradragsberettiget innenlands inngående merverdiavgift, 25 %",
        side: "deduction",
        taxCodeId: 2001,
        base: null,
        exactBase: null,
        rate: null,
        vat: "-2655",
        exactVat: "-2654.43",
        specification: null,
        remark: null,
      },
    ],
  },
  specification: {
    from: "2026-07-01",
    to: "2026-08-31",
    producedAt: STAMP,
    codes: [
      {
        taxCodeId: 2003,
        code: "3",
        name: "Utgående merverdiavgift, 25 %",
        direction: "output",
        reverseCharge: false,
        reported: true,
        group: "sales_domestic",
        ratePercent: "25.00",
        specification: null,
        base: "60000.40",
        outputVat: "15000.10",
        calculatedVat: "0.00",
        deductedVat: "0.00",
        notDeductedVat: "0.00",
        lossVat: "0.00",
        vat: "15000.10",
        postings: 4,
      },
    ],
    notReported: [],
    reconciliation: {
      reconciled: true,
      movement: "-12345.67",
      counted: "-12345.67",
      difference: "0.00",
      accounts: [],
      postings: [],
    },
  },
  checks: {
    ended: true,
    earlierOpen: null,
    attention: 0,
    lockAfterEnd: false,
    reconciled: true,
    partial: false,
    rateMissing: false,
    staleTerm: null,
  },
  closings: [],
  events: [],
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
      openCredits: [],
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
    const refused = state.refusals?.[`${method} ${path}`];
    if (refused) return jsonResponse(refused.status, { ...refused, title: "Refused" });
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
    if (
      method === "PUT" &&
      path === "/lock" &&
      state.bankUnreconciled &&
      !JSON.parse(String(init?.body)).confirmUnreconciled
    )
      return jsonResponse(409, {
        status: 409,
        code: "bank_unreconciled",
        title: "The bank is not reconciled through 2026-10-31",
        detail: "Driftskonto: 2 lines not matched.",
      });
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
    if (supplierId && method === "PUT") {
      const found =
        state.suppliers.find((x) => x.id === Number(supplierId[1])) ?? supplier({ id: Number(supplierId[1]) });
      return jsonResponse(200, { ...found, ...JSON.parse(String(init?.body)), revision: found.revision + 1 });
    }
    const anonymise = /^\/suppliers\/(\d+)\/anonymise$/.exec(path);
    if (anonymise && method === "POST") {
      const found = state.suppliers.find((x) => x.id === Number(anonymise[1])) ?? supplier();
      return jsonResponse(200, {
        ...found,
        name: `Anonymised supplier ${found.number}`,
        anonymisedAt: STAMP,
        active: false,
      });
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
    if (method === "GET" && path === "/vat/terms")
      return jsonResponse(200, {
        vatTerm: state.meta.settings?.vatTerm ?? "bimonthly",
        terms: state.vatTerms ?? [vatTermItem()],
      });
    const term = /^\/vat\/terms\/(\d+)\/(\d+)(\/[a-z]+)?$/.exec(path);
    if (term) {
      const detail = state.vatTerm ?? vatTermDetail();
      const rest = term[3] ?? "";
      if (method === "GET" && rest === "") return jsonResponse(200, detail);
      const closed = {
        ...detail,
        term: {
          ...detail.term,
          state: "closed" as const,
          needsClosing: false,
          closedAt: STAMP,
          settlementVoucher: { id: 5020, display: "2026-000030" },
        },
      };
      if (method === "POST" && rest === "/close") return jsonResponse(200, closed);
      if (method === "POST" && rest === "/reopen") return jsonResponse(200, vatTermDetail());
      if (method === "POST" && rest === "/filing") {
        const body = JSON.parse(String(init?.body));
        return jsonResponse(200, {
          ...closed,
          term: { ...closed.term, filedAt: body.filedAt, filedReference: body.reference, needsFiling: false },
        });
      }
      if (method === "POST" && rest === "/unfile") return jsonResponse(200, closed);
    }
    if (method === "GET" && path === "/vat/postings")
      return jsonResponse(200, {
        total: 1,
        sum: "-60000.40",
        items: [
          {
            voucher: { id: 5001, display: "2026-000007" },
            voucherDate: "2026-07-10",
            voucherDescription: "Salg",
            lineNo: 2,
            accountId: 1005,
            accountNumber: "3000",
            accountName: "Salgsinntekt, avgiftspliktig",
            amount: "-60000.40",
            taxCodeId: 2003,
            taxCode: "3",
            taxRole: "base",
            taxBase: null,
            taxSpecification: null,
            part: "base",
            description: "",
          },
        ],
      });
    if (method === "GET" && path === "/saft-exports/check")
      return jsonResponse(200, {
        fiscalYear: Number(url.searchParams.get("fiscalYear")),
        fromPeriod: Number(url.searchParams.get("fromPeriod")),
        toPeriod: Number(url.searchParams.get("toPeriod")),
        from: "2026-01-01",
        to: "2026-12-31",
        sellerMissing: false,
        vouchers: 12,
        unmappedAccounts: [{ accountId: 1099, number: "7999", name: "Egen konto" }],
      });
    if (method === "GET" && path === "/saft-exports") return jsonResponse(200, { items: [saftExport()] });
    if (method === "POST" && path === "/saft-exports")
      return jsonResponse(202, {
        export: saftExport({ id: 7002, state: "queued", files: [], finishedAt: null, totalBytes: null }),
        unmappedAccounts: [],
      });
    if (method === "GET" && path === "/saft-imports") return jsonResponse(200, { items: [] });
    if (method === "POST" && path === "/saft-imports") return jsonResponse(201, saftImport());
    if (method === "GET" && /^\/saft-imports\/\d+$/.test(path)) return jsonResponse(200, saftImport());
    if (method === "GET" && /^\/saft-imports\/\d+\/customers$/.test(path))
      return jsonResponse(200, { items: saftImport().customers, total: 1 });
    if (method === "PUT" && /^\/saft-imports\/\d+\/customers\/\d+\/link$/.test(path))
      return jsonResponse(200, {
        ...saftImport().customers[0],
        match: "linked",
        customerId: 501,
        customerName: "Acme AS",
        customerOrganisationNumber: "914778271",
      });
    if (method === "GET" && path === "/api/v1/customers")
      return jsonResponse(200, { data: [{ id: 501, name: "Acme AS", customerNumber: 10501 }] });
    if (method === "POST" && /^\/saft-imports\/\d+\/apply$/.test(path))
      return jsonResponse(201, saftImport({ state: "applied", appliedAt: STAMP, openingVoucherId: 5030 }));
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
    if (method === "GET" && path === "/bank/accounts")
      return jsonResponse(200, {
        items: state.bankAccounts ?? [],
        suggested: state.bankSuggested
          ? { name: "Fjord Bygg AS", accountNumber: "15030012345", iban: null, bic: null, ledgerAccountId: 1002 }
          : null,
        feed: { enabled: state.meta.bankFeedEnabled, connected: false, refused: false, provider: null },
      });
    if (method === "POST" && path === "/bank/accounts") return jsonResponse(201, bankAccount());
    const bankOne = /^\/bank\/accounts\/(\d+)$/.exec(path);
    if (method === "GET" && bankOne) return jsonResponse(200, (state.bankAccounts ?? [bankAccount()])[0]);
    if (method === "GET" && /^\/bank\/accounts\/\d+\/reconciliation$/.test(path))
      return jsonResponse(200, reconciliation(state));
    if (method === "PUT" && /^\/bank\/accounts\/\d+\/csv-mapping$/.test(path)) return jsonResponse(200, bankAccount());
    if (method === "POST" && /^\/bank\/accounts\/\d+\/csv-preview$/.test(path))
      return jsonResponse(200, {
        total: 2,
        errors: 0,
        rows: [
          {
            row: 2,
            bookedOn: "2026-11-02",
            amount: "10000.00",
            text: "Innskudd",
            reference: "",
            kid: null,
            counterpartyName: "",
            error: null,
          },
          {
            row: 3,
            bookedOn: "2026-11-03",
            amount: "-12.50",
            text: "Gebyr",
            reference: "",
            kid: null,
            counterpartyName: "",
            error: null,
          },
        ],
      });
    if (method === "GET" && path === "/bank/statements") return jsonResponse(200, { items: [] });
    if (method === "POST" && path === "/bank/imports")
      return jsonResponse(201, {
        statements: [
          {
            bankAccountId: 7001,
            bankAccountName: "Driftskonto",
            statementId: 9001,
            source: "camt053",
            periodFrom: "2026-11-01",
            periodTo: "2026-11-07",
            imported: 5,
            skipped: 1,
            continuity: "gap",
            continuityDetail: "No statement covers 2026-10-25 to 2026-10-31.",
            repeated: false,
            coveredByFeed: 0,
            possibleDuplicates: 2,
          },
        ],
        unknownAccounts: ["30001234567"],
        notBooked: 1,
      });
    const duplicate = /^\/bank\/lines\/(\d+)\/duplicate$/.exec(path);
    if (method === "POST" && duplicate) {
      const found = (state.bankLines ?? []).find((l) => l.id === Number(duplicate[1])) ?? bankLine();
      const body = JSON.parse(String(init?.body));
      return jsonResponse(
        200,
        body.duplicate ? { ...found, state: "duplicate", suggestions: [] } : { ...found, possibleDuplicateOf: null },
      );
    }
    const lineAction = /^\/bank\/lines\/(\d+)\/(accept|code|undo)$/.exec(path);
    if (method === "POST" && lineAction) {
      const found = (state.bankLines ?? []).find((l) => l.id === Number(lineAction[1])) ?? bankLine();
      const matched: AccountingBankLine =
        lineAction[2] === "undo"
          ? { ...found, state: "open", match: null }
          : {
              ...found,
              state: "matched",
              suggestions: [],
              match: {
                id: 1,
                kind: lineAction[2] === "code" ? "coded" : "rule",
                reason: "Coded",
                voucherId: 5002,
                matchedAt: STAMP,
                matchedByUserId: "11111111-1111-1111-1111-111111111111",
                postings: [],
              },
            };
      return jsonResponse(lineAction[2] === "code" ? 201 : 200, matched);
    }
    if (method === "POST" && path === "/bank/lines/accept-exact") {
      const body = JSON.parse(String(init?.body));
      return jsonResponse(200, { accepted: body.lineIds, skipped: [] });
    }
    if (method === "GET" && path === "/bank/status")
      return jsonResponse(200, {
        through: url.searchParams.get("through"),
        reconciled: (state.bankLines ?? []).length === 0,
        accounts: [
          {
            bankAccountId: 7001,
            name: "Driftskonto",
            openLines: (state.bankLines ?? []).length,
            statementBalance: null,
            ledgerBalance: "0.00",
            difference: null,
            reconciled: (state.bankLines ?? []).length === 0,
          },
        ],
      });
    if (method === "GET" && path === "/bank/rules") return jsonResponse(200, { items: state.bankRules ?? [] });
    if (method === "POST" && path === "/bank/rules")
      return jsonResponse(201, {
        id: 1,
        name: "Bankgebyr",
        bankAccountId: null,
        textContains: "gebyr",
        counterpartyContains: null,
        direction: "out",
        accountId: 1006,
        accountNumber: "7770",
        accountName: "Bankgebyrer",
        taxCodeId: null,
        taxCode: null,
        projectId: null,
        description: "",
        active: true,
        version: 1,
      });
    return new Response(null, { status: 404 });
  });
};

/** A finished SAF-T export with one file. */
export const saftExport = (change: Partial<AccountingSaftExport> = {}): AccountingSaftExport => ({
  id: 7001,
  fiscalYear: 2026,
  fromPeriod: 1,
  toPeriod: 12,
  from: "2026-01-01",
  to: "2026-12-31",
  state: "done",
  error: null,
  attempts: 1,
  requestedAt: STAMP,
  requestedBy: "00000000-0000-0000-0000-000000000001",
  requestedByName: "Kari Regnskap",
  startedAt: STAMP,
  finishedAt: STAMP,
  totalBytes: 2048,
  unmappedAccounts: 0,
  files: [
    {
      part: 1,
      parts: 1,
      fileName: "SAF-T Financial_923609016_20261114090000_1_1.xml",
      sizeBytes: 2048,
      sha256: "ab",
      fromPeriod: 1,
      toPeriod: 12,
      from: "2026-01-01",
      to: "2026-12-31",
      removedAt: null,
    },
  ],
  ...change,
});

/** An import's preview: one customer Customers does not hold. */
export const saftImport = (change: Partial<AccountingSaftImport> = {}): AccountingSaftImport => ({
  id: 8001,
  state: "preview",
  fileName: "gammel.xml",
  sizeBytes: 4096,
  sha256: "cd",
  uploadedAt: STAMP,
  appliedAt: null,
  openingVoucherId: null,
  auditFileVersion: "1.40",
  companyName: "Fjord Bygg AS",
  companyOrganisationNumber: "923609016",
  selectionStart: "2025-01-01",
  selectionEnd: "2025-12-31",
  booksStart: "2026-01-01",
  fiscalYearStartMonth: 1,
  transactions: 120,
  canApply: false,
  blockersTotal: 1,
  companyMismatch: false,
  customersTotal: 1,
  suppliersTotal: 1,
  matchedAt: STAMP,
  midYear: false,
  needsFiscalYearStartMonth: true,
  sellerOrganisationNumber: "923609016",
  blockers: ["Customer 15000 Local Retail Inc has a balance of 12500.00 and is unmatched in Customers."],
  notes: ["The file's 120 transactions are not imported as vouchers."],
  accounts: [
    {
      fileAccountId: "1500",
      number: "1500",
      name: "Kundefordringer",
      kind: "asset",
      groupingCategory: "balanseverdiForOmloepsmiddel",
      groupingCode: "1500",
      closingBalance: "12500.00",
      action: "import",
      problem: null,
    },
    {
      fileAccountId: "1920",
      number: "1920",
      name: "Bank",
      kind: "asset",
      groupingCategory: null,
      groupingCode: null,
      closingBalance: "-12500.00",
      action: "import",
      problem: null,
    },
  ],
  addedAccounts: [{ number: "2060", name: "Åpningsbalanse, motkonto", kind: "equity", reason: "rollen opening" }],
  roles: [{ role: "receivables", accountNumber: "1500", added: false }],
  taxCodes: [{ fileTaxCode: "30", description: "Utgående", percentage: "25", standardTaxCode: "3", code: "3" }],
  suppliers: [
    {
      position: 1,
      fileSupplierId: "20001",
      name: "Verksted AS",
      organisationNumber: null,
      city: null,
      bankAccount: null,
      iban: null,
      closingBalance: "0.00",
      notes: [],
      match: "ambiguous",
      supplierId: null,
      supplierName: null,
      supplierNumber: null,
      candidates: [
        { id: 3001, number: 1, name: "Verksted AS", organisationNumber: null },
        { id: 3002, number: 2, name: "Verksted AS", organisationNumber: null },
      ],
    },
  ],
  customers: [
    {
      fileCustomerId: "15000",
      name: "Local Retail Inc",
      organisationNumber: "922222229",
      closingBalance: "12500.00",
      match: "unmatched",
      customerId: null,
      customerName: null,
      customerOrganisationNumber: null,
      position: 1,
    },
  ],
  opening: {
    lines: [
      { accountNumber: "1920", amount: "-12500.00", text: "Bank", party: null },
      { accountNumber: "1500", amount: "12500.00", text: "Inngående saldo 15000", party: "Local Retail Inc" },
    ],
    result: "0.00",
    vatMoved: "0.00",
    vatSettlementAccount: "2740",
    receivablesDifference: "0.00",
    payablesDifference: "0.00",
  },
  ...change,
});

/** The body of a request the fake received. */
export const parseBody = (body: BodyInit | null | undefined): unknown =>
  typeof body === "string" ? JSON.parse(body) : undefined;
