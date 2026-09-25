import type { ExpenseUserRef } from "../api/approvals";
import type { Claim, ClaimCapabilities } from "../api/claims";
import type { Expense, ExpenseAttachment, ExpenseCapabilities } from "../api/entries";
import type { ExpensesMeta } from "../api/meta";
import type { ProjectExpensesBucket, ProjectExpensesCurrency, ProjectExpensesSummary } from "../api/project-expenses";
import type { ExpenseProjectOption } from "../api/projects";
import type { ExpenseRate } from "../api/rates";
import type { ExpenseSettings } from "../api/settings";
import type { ExpenseStats } from "../api/stats";

/**
 * One expense as the store holds it: everything the API renders, plus the one
 * thing the wire cannot carry.
 *
 * `bill_amount` is a nullable column, and **no response says whether it is
 * null**: the server renders `billing` whenever the caller may see it and puts
 * `billAmount: 0` there for a SQL NULL (`responses.go` billingResponse →
 * `floatFromNumeric`), and omits the whole block from a caller who may not.
 * So neither the presence of `billing` nor a zero in it tells a line that is
 * *priced at nothing* from one that is *not priced at all* — and every figure
 * on the project summary that matters here turns on exactly that distinction:
 * `ready` wants `bill_amount IS NOT NULL`, `unpricedCount` counts the NULLs.
 *
 * So a fixture says it outright. `billAmount: null` is the NULL column;
 * a number is the column's value; left out, it follows the `billing` block it
 * was given, which is what most fixtures mean.
 */
export interface StoredExpense extends Expense {
  /** The `bill_amount` **column**: `null` for SQL NULL, a number for a price, absent to follow `billing`. */
  billAmount?: number | null;
}

/** The caller every page test is signed in as. */
export const ME = "11111111-1111-1111-1111-111111111111";

/** Somebody else, whose expenses the caller may approve but never owns. */
export const OTHER = "22222222-2222-2222-2222-222222222222";

/** Whoever the fake records as having decided, overridden or paid something. */
export const APPROVER: ExpenseUserRef = {
  userId: "33333333-3333-3333-3333-333333333333",
  displayName: "Grace Hopper",
  active: true,
};

/** The day every fixture is dated on. */
export const DAY = "2026-09-18";

/** Nothing the caller may do, so a test turns on only what it is about. */
export const noCapabilities: ExpenseCapabilities = {
  canEdit: false,
  canDelete: false,
  canSubmit: false,
  canApprove: false,
  canUnapprove: false,
  canOverrideRate: false,
  canMarkInvoiced: false,
  canUndoInvoiced: false,
  canMarkReimbursed: false,
  canUndoReimbursed: false,
  canSeeBilling: false,
  canSetBilling: false,
};

/** An owner's own draft: theirs to change, delete and submit. */
export const ownDraftCapabilities: ExpenseCapabilities = {
  ...noCapabilities,
  canEdit: true,
  canDelete: true,
  canSubmit: true,
};

export const capabilities = (overrides: Partial<ExpenseCapabilities> = {}): ExpenseCapabilities => ({
  ...noCapabilities,
  ...overrides,
});

const owner = (userId = ME, displayName = "Ada Lovelace") => ({ userId, displayName, active: true });

/** An outlay as the server answers one: the caller's own draft, nothing billed. */
export const outlay = (overrides: Partial<StoredExpense> = {}): StoredExpense => ({
  id: 501,
  kind: "outlay",
  entryDate: DAY,
  description: "Taxi to the airport",
  supplier: "Oslo Taxi",
  category: { id: 11, name: "Travel" },
  currency: "NOK",
  paidBy: "employee",
  grossAmount: 625,
  vatAmount: 125,
  netAmount: 500,
  owedToEmployee: 625,
  billable: false,
  status: "draft",
  attachmentCount: 0,
  attachments: [],
  owner: owner(),
  revision: 1,
  createdAt: "2026-09-18T08:00:00Z",
  updatedAt: "2026-09-18T08:00:00Z",
  capabilities: ownDraftCapabilities,
  ...overrides,
});

/** A mileage line as the server answers one: priced from the dated rate table. */
export const mileage = (overrides: Partial<StoredExpense> = {}): StoredExpense => ({
  id: 601,
  kind: "mileage",
  entryDate: DAY,
  description: "Site visit",
  fromPlace: "Stavanger",
  toPlace: "Bryne",
  distanceKm: 120,
  passengers: 0,
  rate: 5.3,
  currency: "NOK",
  grossAmount: 636,
  netAmount: 636,
  owedToEmployee: 636,
  billable: false,
  status: "draft",
  attachmentCount: 0,
  attachments: [],
  owner: owner(),
  revision: 1,
  createdAt: "2026-09-18T08:00:00Z",
  updatedAt: "2026-09-18T08:00:00Z",
  capabilities: ownDraftCapabilities,
  ...overrides,
});

/**
 * A supplier invoice as the server answers one — a wire literal: the caller's
 * own draft on the panel's project, company-paid and owed to nobody, with the
 * supplier's number and its due date, under Subcontractor. Billing is not on
 * it: an owner's capabilities here cannot see it.
 */
export const supplierInvoice = (overrides: Partial<StoredExpense> = {}): StoredExpense => ({
  id: 551,
  kind: "supplier_invoice",
  entryDate: DAY,
  description: "Rørleggerarbeid, uke 38",
  supplier: "Rør & Varme AS",
  invoiceNumber: "F-20260918",
  dueDate: "2026-10-18",
  category: { id: 14, name: "Subcontractor" },
  currency: "NOK",
  paidBy: "company",
  grossAmount: 12500,
  vatAmount: 2500,
  netAmount: 10000,
  owedToEmployee: 0,
  billable: true,
  project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
  status: "draft",
  attachmentCount: 0,
  attachments: [],
  owner: owner(),
  revision: 1,
  createdAt: "2026-09-18T08:00:00Z",
  updatedAt: "2026-09-18T08:00:00Z",
  capabilities: ownDraftCapabilities,
  ...overrides,
});

export const attachment = (overrides: Partial<ExpenseAttachment> = {}): ExpenseAttachment => ({
  id: 9001,
  fileName: "receipt.jpg",
  contentType: "image/jpeg",
  sizeBytes: 48_000,
  ...overrides,
});

/** An outlay carrying receipts, with the count the server keeps beside the list. */
export const withReceipts = (entry: StoredExpense, attachments: ExpenseAttachment[]): StoredExpense => ({
  ...entry,
  attachments,
  attachmentCount: attachments.length,
});

export const categories: ExpensesMeta["categories"] = [
  { id: 11, name: "Travel", active: true, position: 1 },
  { id: 12, name: "Meals", active: true, position: 2 },
  { id: 13, name: "Old category", active: false, position: 3 },
];

/** The categories with Subcontractor among them, which a new supplier invoice starts under. */
export const categoriesWithSubcontractor: ExpensesMeta["categories"] = [
  ...categories,
  { id: 14, name: "Subcontractor", active: true, position: 4 },
];

/** The installation as the app finds it: projects on, no lock, a receipt threshold. */
export const meta = (overrides: Partial<ExpensesMeta> = {}): ExpensesMeta => ({
  projectsAvailable: true,
  defaultCurrency: "NOK",
  timeZone: "Europe/Oslo",
  categories,
  receiptRequiredOver: 1250,
  capabilities: { canApprove: false, canViewAll: false, canManage: false },
  ...overrides,
});

/** The project the panel tests are about — the first of `projectOptions`. */
export const PROJECT = 1001;

export const summaryBucket = (overrides: Partial<ProjectExpensesBucket> = {}): ProjectExpensesBucket => ({
  count: 0,
  cost: 0,
  billAmount: 0,
  ...overrides,
});

/**
 * One currency of a project's summary. Written out rather than derived so a
 * test can give `total` a figure that is **not** the three buckets added up —
 * which is what the server publishes, each bucket having been rounded once on
 * its own, and the only way to catch a client that adds them itself.
 */
export const summaryCurrency = (overrides: Partial<ProjectExpensesCurrency> = {}): ProjectExpensesCurrency => ({
  currency: "NOK",
  approved: summaryBucket(),
  submitted: summaryBucket(),
  draft: summaryBucket(),
  total: summaryBucket(),
  readyCount: 0,
  readyAmount: 0,
  invoicedCount: 0,
  invoicedAmount: 0,
  unpricedCount: 0,
  ...overrides,
});

/** A project's expense totals as the server answers them; nothing recorded by default. */
export const projectSummary = (overrides: Partial<ProjectExpensesSummary> = {}): ProjectExpensesSummary => ({
  currencies: [],
  capabilities: { canRecord: false },
  ...overrides,
});

export const projectOptions: ExpenseProjectOption[] = [
  {
    id: 1001,
    code: "KVEM1000",
    name: "Kverneland web",
    currency: "NOK",
    billingLines: [
      { id: 3001, code: "PM" },
      { id: 3002, code: "DEV" },
    ],
  },
  { id: 1002, code: "INTERN", name: "Internal", billingLines: [] },
];

/** The rates the product ships with (design §3.4), as the seed writes them. */
export const rates: ExpenseRate[] = [
  { id: 1, kind: "mileage", validFrom: "2026-01-01", value: 5.3, currency: "NOK", source: "State rate" },
  { id: 2, kind: "mileage_passenger", validFrom: "2026-01-01", value: 1, currency: "NOK", source: "State rate" },
];

/**
 * The per diem half of the shipped table (migration 00013). It is kept apart
 * from `rates` so that the tests that are not about travel claims keep the
 * two-row table they were written against; a claim test passes
 * `rates: [...rates, ...perDiemRates]`.
 *
 * `per_diem_overnight_other` is deliberately **not** here, exactly as the
 * migration leaves it unseeded — which is what makes a day of that type
 * refuse until an administrator enters the company's own rate.
 */
export const perDiemRates: ExpenseRate[] = [
  { id: 3, kind: "per_diem_6_12", validFrom: "2026-01-01", value: 397, currency: "NOK", source: "State rate" },
  { id: 4, kind: "per_diem_over_12", validFrom: "2026-01-01", value: 736, currency: "NOK", source: "State rate" },
  {
    id: 5,
    kind: "per_diem_overnight_hotel",
    validFrom: "2026-01-01",
    value: 1012,
    currency: "NOK",
    source: "State rate",
  },
  { id: 6, kind: "meal_breakfast_percent", validFrom: "2026-01-01", value: 20, source: "State rate" },
  { id: 7, kind: "meal_lunch_percent", validFrom: "2026-01-01", value: 30, source: "State rate" },
  { id: 8, kind: "meal_dinner_percent", validFrom: "2026-01-01", value: 50, source: "State rate" },
];

export const settings = (overrides: Partial<ExpenseSettings> = {}): ExpenseSettings => ({
  defaultCurrency: "NOK",
  defaultMarkupPercent: 10,
  receiptRequiredOver: 1250,
  // The installation's business time zone: what every date derived from a
  // travel claim's two instants is taken in, and what a client labels a trip's
  // days with rather than the browser's own zone.
  timeZone: "Europe/Oslo",
  ...overrides,
});

/** A submitted expense of somebody else's that the caller may approve. */
export const submitted = (overrides: Partial<StoredExpense> = {}): StoredExpense =>
  outlay({
    id: 701,
    status: "submitted",
    submittedAt: "2026-09-19T10:00:00Z",
    owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
    capabilities: capabilities({ canApprove: true }),
    ...overrides,
  });

/** Nothing the caller may do to a trip, so a test turns on only what it is about. */
export const noClaimCapabilities: ClaimCapabilities = {
  canEdit: false,
  canDelete: false,
  canSubmit: false,
  canApprove: false,
  canUnapprove: false,
  canMarkReimbursed: false,
  canUndoReimbursed: false,
};

/** The owner's own draft trip: theirs to change, delete and submit. */
export const ownClaimCapabilities: ClaimCapabilities = {
  ...noClaimCapabilities,
  canEdit: true,
  canDelete: true,
  canSubmit: true,
};

export const claimCapabilities = (overrides: Partial<ClaimCapabilities> = {}): ClaimCapabilities => ({
  ...noClaimCapabilities,
  ...overrides,
});

/**
 * A travel claim as the server answers one: the caller's own domestic draft,
 * departing 2026-03-09 07:00 and home 2026-03-11 16:00 in Oslo — the same
 * trip the backend's own harness uses, so a per diem day on the 9th, 10th or
 * 11th falls inside it. `lines` and `totals` are answered by the fake server
 * from the entries store, not from here.
 */
export const claim = (overrides: Partial<Claim> = {}): Claim => ({
  id: 1012,
  purpose: "Montasje hos kunden",
  destination: "Bergen",
  abroad: false,
  departureAt: "2026-03-09T06:00:00Z",
  returnAt: "2026-03-11T15:00:00Z",
  status: "draft",
  owner: owner(),
  lines: [],
  totals: [],
  revision: 1,
  createdAt: "2026-03-08T09:00:00Z",
  updatedAt: "2026-03-08T09:00:00Z",
  capabilities: ownClaimCapabilities,
  ...overrides,
});

/** A per diem day as the server answers one: priced from the dated table, no meal covered. */
export const perDiemLine = (overrides: Partial<StoredExpense> = {}): StoredExpense => ({
  id: 801,
  claimId: 1012,
  kind: "per_diem",
  entryDate: "2026-03-09",
  description: "",
  currency: "NOK",
  grossAmount: 1012,
  netAmount: 1012,
  owedToEmployee: 1012,
  rate: 1012,
  billable: false,
  status: "draft",
  attachmentCount: 0,
  attachments: [],
  owner: owner(),
  revision: 1,
  createdAt: "2026-03-09T09:00:00Z",
  updatedAt: "2026-03-09T09:00:00Z",
  capabilities: ownDraftCapabilities,
  perDiem: {
    type: "overnight_hotel",
    breakfastCovered: false,
    lunchCovered: false,
    dinnerCovered: false,
    dayRate: 1012,
    mealPercents: { breakfast: 20, lunch: 30, dinner: 50 },
  },
  ...overrides,
});

export const stats = (overrides: Partial<ExpenseStats> = {}): ExpenseStats => ({
  draft: 0,
  submitted: 0,
  approved: 0,
  rejected: 0,
  unreimbursed: [],
  awaitingMyApproval: 0,
  ...overrides,
});
