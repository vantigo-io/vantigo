import type { ExpenseUserRef } from "../api/approvals";
import type { Expense, ExpenseAttachment, ExpenseCapabilities } from "../api/entries";
import type { ExpensesMeta } from "../api/meta";
import type { ExpenseProjectOption } from "../api/projects";
import type { ExpenseRate } from "../api/rates";
import type { ExpenseSettings } from "../api/settings";
import type { ExpenseStats } from "../api/stats";

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
export const outlay = (overrides: Partial<Expense> = {}): Expense => ({
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
export const mileage = (overrides: Partial<Expense> = {}): Expense => ({
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

export const attachment = (overrides: Partial<ExpenseAttachment> = {}): ExpenseAttachment => ({
  id: 9001,
  fileName: "receipt.jpg",
  contentType: "image/jpeg",
  sizeBytes: 48_000,
  ...overrides,
});

/** An outlay carrying receipts, with the count the server keeps beside the list. */
export const withReceipts = (entry: Expense, attachments: ExpenseAttachment[]): Expense => ({
  ...entry,
  attachments,
  attachmentCount: attachments.length,
});

export const categories: ExpensesMeta["categories"] = [
  { id: 11, name: "Travel", active: true, position: 1 },
  { id: 12, name: "Meals", active: true, position: 2 },
  { id: 13, name: "Old category", active: false, position: 3 },
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
export const submitted = (overrides: Partial<Expense> = {}): Expense =>
  outlay({
    id: 701,
    status: "submitted",
    submittedAt: "2026-09-19T10:00:00Z",
    owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
    capabilities: capabilities({ canApprove: true }),
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
