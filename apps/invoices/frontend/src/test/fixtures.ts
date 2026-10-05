import type { InvoiceDocument, InvoiceList } from "../api/invoices";
import type { InvoiceJournal } from "../api/journal";
import type { InvoicesMeta } from "../api/meta";
import type { InvoiceSettings } from "../api/settings";
import type { VatCode } from "../api/vat-codes";
import type { components } from "../api-schema";

/**
 * GET /meta as the server sends it — a wire literal: a complete seller, mail
 * available, the caller may create, issue, register payments and send, and
 * today is 2026-09-12 in Oslo.
 */
export const meta = (overrides: Partial<InvoicesMeta> = {}): InvoicesMeta => ({
  currency: "NOK",
  defaultPaymentTermsDays: 14,
  sellerComplete: true,
  missingSellerFields: [],
  anythingIssued: true,
  seriesStart: 1,
  storageAvailable: true,
  mailAvailable: true,
  ehfAvailable: false,
  accessPointCredentialsRejected: false,
  today: "2026-09-12",
  vatCodes: [
    { id: 1, code: "3", name: "Utgående mva 25 %", safTCode: "3", ehfCategory: "S", ratePercent: 25 },
    { id: 2, code: "31", name: "Utgående mva 15 %", safTCode: "31", ehfCategory: "S", ratePercent: 15 },
    {
      id: 5,
      code: "5",
      name: "Fritatt innenlands 0 %",
      safTCode: "5",
      ehfCategory: "Z",
      exemptionReason: "Fritatt for merverdiavgift",
      ratePercent: 0,
    },
  ],
  capabilities: {
    canCreate: true,
    canIssue: true,
    canManage: false,
    canRegisterPayments: true,
    canSend: true,
    canSendEhf: false,
  },
  workAvailable: true,
  work: { hours: true, expenses: true, milestones: true },
  ...overrides,
});

/** An invoice draft for Acme with three lines of 33.33 at 25 %, as the server answers it. */
export const draft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => ({
  id: 1001,
  kind: "invoice",
  status: "draft",
  state: "draft",
  customerId: 2001,
  customerName: "Acme AS",
  deliveryDate: "2026-09-10",
  paymentTermsDays: 30,
  currency: "NOK",
  exchangeRate: 1,
  yourReference: "PO-77",
  ourReference: "Ola Nordmann",
  orderReference: "",
  note: "",
  internalNote: "",
  netTotal: 99.99,
  vatTotal: 25,
  grossTotal: 124.99,
  vatTotalNok: 25,
  lines: [1, 2, 3].map((position) => ({
    id: 5000 + position,
    position,
    description: `Tredjedel ${position}`,
    quantity: 1,
    unit: "timer",
    unitPrice: 33.33,
    discountPercent: 0,
    vatCodeId: 1,
    lineGross: 33.33,
    lineAllowance: 0,
    lineNet: 33.33,
  })),
  vatSummaries: [
    { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 99.99, vatAmount: 25, vatAmountNok: 25 },
  ],
  warnings: [],
  allowedIssueDates: ["2026-09-12"],
  createdAt: "2026-09-12T10:00:00Z",
  updatedAt: "2026-09-12T10:00:00Z",
  revision: 3,
  ...overrides,
});

/**
 * An invoice draft made from work (invoices work design D2), as a save answers
 * it: line 1 bills two hour entries and is written down below their sum, line 2
 * a mileage expense and line 3 nothing; the save released a milestone. All of
 * its work is project 41's, so the save derived the document's project (D9).
 */
export const workDraft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = draft();
  const held = { projectId: 41, state: "held" };
  return {
    ...base,
    lines: [
      {
        ...base.lines[0],
        sources: [
          { ...held, kind: "time.entry", id: 501, date: "2026-09-01", quantity: 4, amount: 4800 },
          { ...held, kind: "time.entry", id: 502, date: "2026-09-02", quantity: 3.5, amount: 4200 },
        ],
        warnings: ["line_differs_from_sources"],
      },
      {
        ...base.lines[1],
        sources: [{ ...held, kind: "expenses.entry", id: 601, date: "2026-09-03", quantity: 90, amount: 450 }],
        warnings: [],
      },
      { ...base.lines[2], sources: [], warnings: [] },
    ],
    sources: { count: 3, held: 3, invoiced: 0, released: 0 },
    projectId: 41,
    projectReference: "P-41",
    releasedSources: [{ kind: "projects.milestone", id: 701 }],
    warnings: ["line_differs_from_sources", "sources_released"],
    ...overrides,
  };
};

type WorkView = components["schemas"]["InvoicesWorkResponse"];

/**
 * GET /invoices/work for Acme as the server answers it (invoices work design
 * D3): project P-41 with Kari's 4 h selectable and Ola's 3.5 h held by draft
 * 1001, a mileage expense and a ready milestone; the fixed-price project
 * P-42's hours shown, not selectable; the totals over the selectable work.
 */
export const workView = (overrides: Partial<WorkView> = {}): WorkView => ({
  customerId: 2001,
  projects: [
    {
      id: 41,
      code: "P-41",
      name: "Apollo",
      billingType: "time-and-materials",
      currency: "NOK",
      hours: [
        {
          id: 801,
          revision: 2,
          date: "2026-09-01",
          userId: "00000000-0000-0000-0000-00000000000a",
          hours: 4,
          billRate: 1200,
          rate: 1200,
          amount: 4800,
          currency: "NOK",
          workTypeId: 5,
          workTypeName: "Utvikling",
          selectable: true,
        },
        {
          id: 802,
          revision: 1,
          date: "2026-09-02",
          userId: "00000000-0000-0000-0000-00000000000b",
          hours: 3.5,
          billRate: 1200,
          rate: 1200,
          amount: 4200,
          currency: "NOK",
          selectable: false,
          reason: "held",
          heldBy: { invoiceId: 1001, status: "draft" },
        },
      ],
      expenses: [
        {
          id: 902,
          revision: 1,
          kind: "mileage",
          date: "2026-09-04",
          description: "Oslo–Drammen",
          netAmount: 405,
          distanceKm: 90,
          billRatePerKm: 4.5,
          billAmount: 405,
          currency: "NOK",
          selectable: true,
        },
      ],
      milestones: [
        {
          id: 951,
          revision: 1,
          name: "Fase 1",
          description: "",
          readyAt: "2026-09-05T10:00:00Z",
          date: "2026-09-05",
          amount: 10000,
          currency: "NOK",
          selectable: true,
        },
      ],
      heldOnDrafts: [{ invoiceId: 1001, kind: "time.entry", count: 1 }],
      warnings: [],
    },
    {
      id: 42,
      code: "P-42",
      name: "Borealis",
      billingType: "fixed-price",
      currency: "NOK",
      hours: [
        {
          id: 803,
          revision: 1,
          date: "2026-07-03",
          userId: "00000000-0000-0000-0000-00000000000a",
          hours: 2,
          billRate: 1000,
          rate: 1000,
          amount: 2000,
          currency: "NOK",
          selectable: false,
          reason: "fixed_price",
        },
      ],
      expenses: [],
      milestones: [],
      heldOnDrafts: [],
      warnings: [],
    },
  ],
  totals: [{ currency: "NOK", amount: 15205 }],
  users: [
    { id: "00000000-0000-0000-0000-00000000000a", displayName: "Kari Nordmann" },
    { id: "00000000-0000-0000-0000-00000000000b", displayName: "Ola Hansen" },
  ],
  warnings: [],
  ...overrides,
});

/**
 * POST /invoices/from-work's answer (201): a new Acme draft of Kari's hours,
 * the mileage and the milestone grouped by project, in Norwegian, every line
 * holding its work.
 */
export const fromWorkDraft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const { deliveryDate: _deliveryDate, ...base } = draft();
  const held = { projectId: 41, state: "held" };
  const line = { discountPercent: 0, vatCodeId: 1, warnings: [] };
  return {
    ...base,
    id: 1002,
    deliveryFrom: "2026-09-01",
    deliveryTo: "2026-09-05",
    lines: [
      {
        ...line,
        id: 5101,
        position: 1,
        description: "Konsulenttimer, Apollo, 1. sep. 2026",
        quantity: 4,
        unit: "timer",
        unitPrice: 1200,
        lineGross: 4800,
        lineAllowance: 0,
        lineNet: 4800,
        sources: [{ ...held, kind: "time.entry", id: 801, date: "2026-09-01", quantity: 4, amount: 4800 }],
      },
      {
        ...line,
        id: 5102,
        position: 2,
        description: "Kjøregodtgjørelse, Apollo, 4. sep. 2026",
        quantity: 1,
        unit: "",
        unitPrice: 405,
        lineGross: 405,
        lineAllowance: 0,
        lineNet: 405,
        sources: [{ ...held, kind: "expenses.entry", id: 902, date: "2026-09-04", quantity: 90, amount: 405 }],
      },
      {
        ...line,
        id: 5103,
        position: 3,
        description: "Fase 1",
        quantity: 1,
        unit: "",
        unitPrice: 10000,
        lineGross: 10000,
        lineAllowance: 0,
        lineNet: 10000,
        sources: [{ ...held, kind: "projects.milestone", id: 951, date: "2026-09-05", quantity: 1, amount: 10000 }],
      },
    ],
    vatSummaries: [
      {
        vatCategory: "S",
        ratePercent: 25,
        safTCode: "3",
        taxableAmount: 15205,
        vatAmount: 3801.25,
        vatAmountNok: 3801.25,
      },
    ],
    netTotal: 15205,
    vatTotal: 3801.25,
    grossTotal: 19006.25,
    vatTotalNok: 3801.25,
    sources: { count: 3, held: 3, invoiced: 0, released: 0 },
    warnings: [],
    ...overrides,
  };
};

/**
 * A final settlement draft (invoices work design D7), as a save answers it:
 * the work, and a deduction of a-konto invoice 985 (id 990) at 25 % — quantity
 * -1, the amount deducted as a positive unit price, its line amounts negative.
 */
export const settlementDraft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = draft();
  return {
    ...base,
    netTotal: 50000,
    vatTotal: 12500,
    grossTotal: 62500,
    vatTotalNok: 12500,
    lines: [
      { ...base.lines[0], description: "Sluttoppgjør", unitPrice: 150000, lineGross: 150000, lineNet: 150000 },
      {
        id: 5002,
        position: 2,
        description: "Tidligere fakturert a konto, faktura 985",
        quantity: -1,
        unit: "",
        unitPrice: 100000,
        discountPercent: 0,
        vatCodeId: 1,
        deductsInvoiceId: 990,
        lineGross: -100000,
        lineAllowance: 0,
        lineNet: -100000,
      },
    ],
    vatSummaries: [
      { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 50000, vatAmount: 12500, vatAmountNok: 12500 },
    ],
    ...overrides,
  };
};

/**
 * GET /invoices/1001/deductible as the server answers it (D7): what a-konto
 * 985 has left to deduct, per VAT code, with its lines' snapshot.
 */
export const deductible = (): components["schemas"]["InvoicesDeductible"][] => [
  { invoiceId: 990, number: 985, issueDate: "2026-08-01", vatCodeId: 1, category: "S", ratePercent: 25, left: 100000 },
  { invoiceId: 990, number: 985, issueDate: "2026-08-01", vatCodeId: 2, category: "S", ratePercent: 15, left: 40000 },
];

/** The same invoice, issued as number 1000, nothing credited or paid yet: open. */
export const issued = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = draft();
  return {
    ...base,
    status: "issued",
    state: "open",
    number: 1000,
    issueDate: "2026-09-12",
    dueDate: "2026-10-12",
    exchangeRateDate: "2026-09-12",
    customerName: "Acme Norge AS",
    issuedAt: "2026-09-12T10:30:00Z",
    issuedByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
    buyer: {
      customerNumber: 10001,
      type: "business",
      name: "Acme Norge AS",
      organisationNumber: "923609016",
      addressLine1: "Kundeveien 2",
      postalCode: "0150",
      city: "Oslo",
      country: "NO",
      peppolId: "0192:923609016",
      gln: "7080000000001",
      language: "nb",
    },
    seller: {
      legalName: "Kraft-Verket AS",
      organisationNumber: "974760673",
      vatRegistered: true,
      inForetaksregisteret: true,
      addressLine1: "Storgata 1",
      addressLine2: "",
      postalCode: "0155",
      city: "Oslo",
      country: "NO",
      bankAccount: "15032080119",
      iban: "",
      bic: "",
      email: "faktura@kraft-verket.no",
      footerText: "",
    },
    lines: base.lines.map((l) => ({ ...l, vatRatePercent: 25, vatCategory: "S", safTCode: "3" })),
    allowedIssueDates: undefined,
    pdfStored: true,
    creditedAmount: 0,
    uncreditedAmount: 124.99,
    creditNotes: [],
    paidAmount: 0,
    openAmount: 124.99,
    payments: [],
    deliveries: [],
    sendDefaults: { recipient: "faktura@acme.no", warnings: ["buyer_norwegian_business"] },
    ehf: { status: "not_sent", canSend: false, blockedBy: "ehf_unavailable", transmissions: [] },
    revision: 4,
    ...overrides,
  };
};

/**
 * Invoice 1000 as the server sends it once money has come in — a wire literal:
 * issued on 1 September, due on 1 October, 124.99 with one live payment of 50
 * and one of 20 removed with its reason, so 74.99 is open; sent twice, the
 * second time to an override; and the Send dialog's defaults for a sender.
 */
export const partlyPaid = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => ({
  id: 1001,
  kind: "invoice",
  status: "issued",
  state: "partially_paid",
  number: 1000,
  customerId: 2001,
  customerName: "Acme Norge AS",
  issueDate: "2026-09-01",
  dueDate: "2026-10-01",
  deliveryDate: "2026-08-31",
  paymentTermsDays: 30,
  currency: "NOK",
  exchangeRate: 1,
  exchangeRateDate: "2026-09-01",
  yourReference: "PO-77",
  ourReference: "Ola Nordmann",
  orderReference: "",
  note: "",
  internalNote: "",
  netTotal: 99.99,
  vatTotal: 25,
  grossTotal: 124.99,
  vatTotalNok: 25,
  lines: [
    {
      id: 5001,
      position: 1,
      description: "Konsulenttimer",
      quantity: 1,
      unit: "timer",
      unitPrice: 99.99,
      discountPercent: 0,
      vatCodeId: 1,
      vatRatePercent: 25,
      vatCategory: "S",
      safTCode: "3",
      lineGross: 99.99,
      lineAllowance: 0,
      lineNet: 99.99,
    },
  ],
  vatSummaries: [
    { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 99.99, vatAmount: 25, vatAmountNok: 25 },
  ],
  warnings: [],
  buyer: {
    customerNumber: 10001,
    type: "business",
    name: "Acme Norge AS",
    organisationNumber: "923609016",
    addressLine1: "Kundeveien 2",
    postalCode: "0150",
    city: "Oslo",
    country: "NO",
    language: "nb",
  },
  seller: {
    legalName: "Kraft-Verket AS",
    organisationNumber: "974760673",
    vatRegistered: true,
    inForetaksregisteret: true,
    addressLine1: "Storgata 1",
    addressLine2: "",
    postalCode: "0155",
    city: "Oslo",
    country: "NO",
    bankAccount: "15032080119",
    iban: "",
    bic: "",
    email: "faktura@kraft-verket.no",
    footerText: "",
  },
  issuedAt: "2026-09-01T09:00:00Z",
  issuedByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
  pdfStored: true,
  creditedAmount: 0,
  uncreditedAmount: 124.99,
  creditNotes: [],
  paidAmount: 50,
  openAmount: 74.99,
  payments: [
    {
      id: 1001,
      paidOn: "2026-09-05",
      amount: 20,
      currency: "NOK",
      reference: "Feil KID",
      note: "",
      registeredAt: "2026-09-05T08:00:00Z",
      registeredByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
      removedAt: "2026-09-06T08:00:00Z",
      removedByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
      removalReason: "Registrert på feil faktura",
    },
    {
      id: 1002,
      paidOn: "2026-09-10",
      amount: 50,
      currency: "NOK",
      reference: "Bank 4471",
      note: "Første avdrag",
      registeredAt: "2026-09-10T12:00:00Z",
      registeredByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
    },
  ],
  deliveries: [
    {
      id: 1001,
      recipient: "faktura@acme.no",
      sentAt: "2026-09-01T09:05:00Z",
      sentByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
      subject: "Faktura 1000 fra Kraft-Verket AS",
    },
    {
      id: 1002,
      recipient: "regnskap@acme.no",
      sentAt: "2026-09-11T10:00:00Z",
      sentByUserId: "0b6e4c1a-5f7d-4d8e-9a3b-2c1d0e9f8a7b",
      subject: "Faktura 1000 fra Kraft-Verket AS",
    },
  ],
  sendDefaults: { recipient: "faktura@acme.no", preference: "email", warnings: ["buyer_norwegian_business"] },
  // Sent as EHF once too: delivered, so another send is blocked.
  ehf: {
    status: "delivered",
    queuedAt: "2026-09-01T09:10:00Z",
    submittedAt: "2026-09-01T09:10:05Z",
    deliveredAt: "2026-09-01T09:12:00Z",
    providerRef: "6c1f0b52-9d3e-4f0a-8a1b-2f7c3e4d5a6b",
    canSend: false,
    blockedBy: "ehf_already_sent",
    preference: "email",
    buyerPeppolId: "0192:923609016",
    transmissions: [
      {
        id: 1001,
        status: "delivered",
        provider: "storecove",
        idempotencyKey: "3f2a9c1e-7b4d-4e8f-9a0b-1c2d3e4f5a6b",
        receiverParticipant: "0192:923609016",
        ublSha256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
        queuedAt: "2026-09-01T09:10:00Z",
        submittedAt: "2026-09-01T09:10:05Z",
        deliveredAt: "2026-09-01T09:12:00Z",
        providerRef: "6c1f0b52-9d3e-4f0a-8a1b-2f7c3e4d5a6b",
        ublUrl: "/api/v1/invoices/1001/transmissions/1001/ubl",
      },
    ],
  },
  createdAt: "2026-08-31T10:00:00Z",
  updatedAt: "2026-09-01T09:00:00Z",
  revision: 4,
  ...overrides,
});

/** A credit-note draft of invoice 1000, crediting its first line. */
export const creditDraft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = draft();
  return {
    ...base,
    id: 1002,
    kind: "credit_note",
    paymentTermsDays: undefined,
    customerName: "Acme Norge AS",
    buyer: {
      customerNumber: 10001,
      type: "business",
      name: "Acme Norge AS",
      organisationNumber: "923609016",
      language: "nb",
    },
    lines: [{ ...base.lines[0], id: 6001, creditsLineId: 5001 }],
    netTotal: 33.33,
    vatTotal: 8.33,
    grossTotal: 41.66,
    vatTotalNok: 8.33,
    vatSummaries: [
      { vatCategory: "S", ratePercent: 25, safTCode: "3", taxableAmount: 33.33, vatAmount: 8.33, vatAmountNok: 8.33 },
    ],
    credits: { id: 1001, number: 1000, issueDate: "2026-09-12" },
    revision: 1,
    ...overrides,
  };
};

/**
 * A credit-note draft of an invoice made from work (invoices work design D8):
 * it returns the hours line in full, so its issue would release both hour
 * entries; it holds no work of its own, so its counts are zero.
 */
export const workCreditDraft = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument =>
  creditDraft({
    sources: {
      count: 0,
      held: 0,
      invoiced: 0,
      released: 0,
      wouldRelease: [
        { kind: "time.entry", id: 501 },
        { kind: "time.entry", id: 502 },
      ],
    },
    ...overrides,
  });

/**
 * Invoice 1000 made from work, after a credit note returned its hours line in
 * full (D8): the two hour entries are released, the mileage still invoiced.
 */
export const releasedWork = (overrides: Partial<InvoiceDocument> = {}): InvoiceDocument => {
  const base = issued();
  const source = { projectId: 41 };
  return {
    ...base,
    lines: [
      {
        ...base.lines[0],
        sources: [
          { ...source, kind: "time.entry", id: 501, date: "2026-09-01", quantity: 4, amount: 4800, state: "released" },
          {
            ...source,
            kind: "time.entry",
            id: 502,
            date: "2026-09-02",
            quantity: 3.5,
            amount: 4200,
            state: "released",
          },
        ],
        warnings: [],
      },
      {
        ...base.lines[1],
        sources: [
          {
            ...source,
            kind: "expenses.entry",
            id: 601,
            date: "2026-09-03",
            quantity: 90,
            amount: 450,
            state: "invoiced",
          },
        ],
        warnings: [],
      },
      { ...base.lines[2], sources: [], warnings: [] },
    ],
    sources: { count: 3, held: 0, invoiced: 1, released: 2 },
    ...overrides,
  };
};

/**
 * A page of the list: a draft, then two issued documents — the invoice's work
 * all project 41's, the project a list may be filtered on (D9).
 */
export const listPage = (overrides: Partial<InvoiceList["pagination"]> = {}): InvoiceList => ({
  data: [
    {
      id: 1003,
      kind: "invoice",
      status: "draft",
      state: "draft",
      customerId: 2002,
      customerName: "Kari Nordmann",
      currency: "NOK",
      grossTotal: 0,
    },
    {
      id: 1002,
      kind: "credit_note",
      status: "issued",
      state: "issued",
      number: 1001,
      customerId: 2001,
      customerName: "Acme Norge AS",
      issueDate: "2026-09-12",
      currency: "NOK",
      grossTotal: 41.66,
      creditsInvoiceId: 1001,
      ehfStatus: "not_sent",
    },
    {
      id: 1001,
      kind: "invoice",
      status: "issued",
      state: "open",
      number: 1000,
      customerId: 2001,
      customerName: "Acme Norge AS",
      issueDate: "2026-09-12",
      dueDate: "2026-10-12",
      currency: "NOK",
      grossTotal: 124.99,
      openAmount: 124.99,
      ehfStatus: "delivered",
      projectId: 41,
      projectReference: "P-41",
    },
  ],
  pagination: {
    page: 1,
    pageSize: 25,
    totalCount: 3,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
    ...overrides,
  },
});

/**
 * GET /vat-codes as the server sends it: every code, inactive ones included.
 * Code 3 is at 25 % with a change to 26 % from 2027, not yet in force; code 9
 * is no longer offered but still has a period covering today.
 */
export const vatCodes = (): VatCode[] => [
  {
    id: 1,
    code: "3",
    name: "Utgående mva 25 %",
    safTCode: "3",
    ehfCategory: "S",
    active: true,
    inUse: true,
    revision: 1,
    rates: [
      { id: 1001, ratePercent: 25, validFrom: "2026-01-01", validTo: "2026-12-31" },
      { id: 1002, ratePercent: 26, validFrom: "2027-01-01" },
    ],
  },
  {
    id: 2,
    code: "31",
    name: "Utgående mva 15 %",
    safTCode: "31",
    ehfCategory: "S",
    active: true,
    inUse: false,
    revision: 1,
    rates: [{ id: 1003, ratePercent: 15, validFrom: "2026-01-01" }],
  },
  {
    id: 5,
    code: "5",
    name: "Fritatt innenlands 0 %",
    safTCode: "5",
    ehfCategory: "Z",
    exemptionReason: "Fritatt for merverdiavgift",
    active: true,
    inUse: false,
    revision: 1,
    rates: [{ id: 1005, ratePercent: 0, validFrom: "2026-01-01" }],
  },
  {
    id: 9,
    code: "3G",
    name: "Gammel sats",
    safTCode: "3",
    ehfCategory: "S",
    active: false,
    inUse: true,
    revision: 2,
    rates: [{ id: 1009, ratePercent: 25, validFrom: "2020-01-01" }],
  },
];

/** The settings as the server sends them: a seller lacking two fields, the series locked. */
export const settings = (overrides: Partial<InvoiceSettings> = {}): InvoiceSettings => ({
  legalName: "Kraft-Verket AS",
  organisationNumber: "974760673",
  vatRegistered: true,
  inForetaksregisteret: true,
  addressLine1: "Storgata 1",
  addressLine2: "",
  postalCode: "",
  city: "Oslo",
  country: "NO",
  bankAccount: "",
  iban: "",
  bic: "",
  email: "faktura@kraft-verket.no",
  defaultPaymentTermsDays: 14,
  defaultCurrency: "NOK",
  footerText: "",
  seriesStart: 1000,
  seriesLocked: true,
  nextNumber: 1003,
  peppolId: null,
  kidLength: null,
  kidAlgorithm: null,
  missingSellerFields: ["postalCode", "bankAccount"],
  warnings: [],
  workVatCodes: { hours: 1, expenses: 1, milestones: 1 },
  revision: 5,
  updatedAt: "2026-09-12T10:00:00Z",
  ...overrides,
});

type AccessPointResponse = components["schemas"]["InvoicesAccessPointResponse"];

/** PUT /settings/access-point's answer: Storecove credentials stored, never the key. */
export const accessPoint = (overrides: Partial<AccessPointResponse> = {}): AccessPointResponse => ({
  provider: "storecove",
  legalEntityId: 4711,
  hasCredentials: true,
  ...overrides,
});

/** A month's journal: numbers 1000 to 1002, a credit note signed negative. */
export const journal = (overrides: Partial<InvoiceJournal> = {}): InvoiceJournal => ({
  data: [
    {
      id: 1,
      number: 1000,
      kind: "invoice",
      issueDate: "2026-09-02",
      currency: "NOK",
      buyerName: "Acme Norge AS",
      netTotal: 1000,
      vatTotal: 250,
      grossTotal: 1250,
      vatSummaries: [{ safTCode: "3", category: "S", ratePercent: 25, taxableAmount: 1000, vatAmount: 250 }],
    },
    {
      id: 2,
      number: 1001,
      kind: "invoice",
      issueDate: "2026-09-05",
      currency: "NOK",
      buyerName: "Kari Nordmann",
      netTotal: 200,
      vatTotal: 0,
      grossTotal: 200,
      vatSummaries: [{ safTCode: "5", category: "Z", ratePercent: 0, taxableAmount: 200, vatAmount: 0 }],
    },
    {
      id: 3,
      number: 1002,
      kind: "credit_note",
      issueDate: "2026-09-10",
      currency: "NOK",
      buyerName: "Acme Norge AS",
      netTotal: -400,
      vatTotal: -100,
      grossTotal: -500,
      creditsNumber: 1000,
      vatSummaries: [{ safTCode: "3", category: "S", ratePercent: 25, taxableAmount: -400, vatAmount: -100 }],
    },
  ],
  pagination: { page: 1, pageSize: 25, totalCount: 3, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
  totals: {
    byCode: [
      { safTCode: "3", category: "S", ratePercent: 25, taxableAmount: 600, vatAmount: 150 },
      { safTCode: "5", category: "Z", ratePercent: 0, taxableAmount: 200, vatAmount: 0 },
    ],
    netTotal: 800,
    vatTotal: 150,
    grossTotal: 950,
  },
  gaps: [],
  gapsTruncated: false,
  seriesStart: 1000,
  counterLast: 1002,
  highestIssued: 1002,
  checkedFrom: 1000,
  checkedTo: 1002,
  ...overrides,
});

type EhfState = components["schemas"]["InvoicesEhfState"];
type Transmission = components["schemas"]["InvoicesTransmission"];

/** Meta for an installation that can send EHF, to a caller who may: `ehfAvailable` and `canSendEhf`. */
export const ehfMeta = (overrides: Partial<InvoicesMeta> = {}): InvoicesMeta =>
  meta({ ehfAvailable: true, capabilities: { ...meta().capabilities, canSendEhf: true }, ...overrides });

/** One EHF transmission of document 1001 as the server sends it — a wire literal, queued at 11:00 on 12 September. */
export const transmission = (overrides: Partial<Transmission> = {}): Transmission => ({
  id: 1101,
  status: "queued",
  provider: "storecove",
  idempotencyKey: "5b0c7e2a-1d3f-4a6b-8c9d-0e1f2a3b4c5d",
  receiverParticipant: "0192:923609016",
  ublSha256: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
  queuedAt: "2026-09-12T11:00:00Z",
  ublUrl: "/api/v1/invoices/1001/transmissions/1101/ubl",
  ...overrides,
});

/** The same transmission once a worker has stamped its crash marker: attempted, so no longer cancellable. */
export const attemptedTransmission = (overrides: Partial<Transmission> = {}): Transmission =>
  transmission({ submitAttemptedAt: "2026-09-12T11:00:04Z", ...overrides });

/** The EHF states an issued document's `ehf` block can be in (EHF and KID design D10). */
export type EhfStatus = "not_sent" | "queued" | "submitted" | "delivered" | "failed" | "unconfirmed" | "cancelled";

/**
 * Document 1001's `ehf` block in `status`, as the server sends it to a caller
 * with `invoices:issue`: the customer prefers EHF and has a Peppol id; the
 * latest transmission's timestamps, reference and reason; `canSend` and
 * `blockedBy` as D8 would judge them; the transmissions newest first.
 */
export const ehfState = (status: EhfStatus, overrides: Partial<EhfState> = {}): EhfState => {
  const profile = { preference: "ehf", buyerPeppolId: "0192:923609016" };
  const blocked = { canSend: false, blockedBy: "ehf_already_sent" };
  const submitted = {
    submittedAt: "2026-09-12T11:00:05Z",
    providerRef: "8d4e2f1a-3b5c-4d6e-9f0a-1b2c3d4e5f6a",
  };
  const states: Record<EhfStatus, EhfState> = {
    not_sent: { status, canSend: true, ...profile, transmissions: [] },
    queued: { status, queuedAt: "2026-09-12T11:00:00Z", ...blocked, ...profile, transmissions: [transmission()] },
    submitted: {
      status,
      queuedAt: "2026-09-12T11:00:00Z",
      ...submitted,
      ...blocked,
      ...profile,
      transmissions: [transmission({ status, ...submitted })],
    },
    delivered: {
      status,
      queuedAt: "2026-09-12T11:00:00Z",
      ...submitted,
      deliveredAt: "2026-09-12T11:02:00Z",
      ...blocked,
      ...profile,
      transmissions: [transmission({ status, ...submitted, deliveredAt: "2026-09-12T11:02:00Z" })],
    },
    failed: {
      status,
      queuedAt: "2026-09-12T11:00:00Z",
      failedAt: "2026-09-12T11:00:06Z",
      reason: "Storecove: the receiver rejected the document",
      canSend: true,
      ...profile,
      transmissions: [
        transmission({
          status,
          failedAt: "2026-09-12T11:00:06Z",
          reason: "Storecove: the receiver rejected the document",
        }),
      ],
    },
    unconfirmed: {
      status,
      queuedAt: "2026-09-12T11:00:00Z",
      ...submitted,
      ...blocked,
      ...profile,
      transmissions: [transmission({ status, ...submitted })],
    },
    cancelled: {
      status,
      queuedAt: "2026-09-12T11:00:00Z",
      canSend: true,
      ...profile,
      transmissions: [transmission({ status, cancelledAt: "2026-09-12T11:00:30Z" })],
    },
  };
  return { ...states[status], ...overrides };
};

/** Document 1001 (invoice number 1000), issued, with its `ehf` block in `status`. */
export const ehfDocument = (status: EhfStatus, ehf: Partial<EhfState> = {}, overrides: Partial<InvoiceDocument> = {}) =>
  issued({ ehf: ehfState(status, ehf), ...overrides });
