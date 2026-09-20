import type { ExpenseApprovalGroup, ExpenseBillingLineOption, ExpenseCurrencyTotal } from "../api/approvals";
import type { ExpenseCategory } from "../api/categories";
import type { Claim, ClaimListItem, ClaimSummary, PerDiemSuggestedDay } from "../api/claims";
import type { Expense, ExpenseAttachment, ExpenseInput, ExpenseUpdateInput } from "../api/entries";
import type { ExpensesMeta } from "../api/meta";
import type { ProjectExpensesBucket, ProjectExpensesCurrency, ProjectExpensesSummary } from "../api/project-expenses";
import type { ExpenseProjectOption } from "../api/projects";
import type { ExpenseRate } from "../api/rates";
import type { ExpenseReimbursementGroup } from "../api/reimbursements";
import type { ExpenseSettings } from "../api/settings";
import type { ExpenseStats } from "../api/stats";
import { round2 } from "../lib/money";
import { meals, type PerDiem, type PerDiemType } from "../lib/per-diem";
import { mileagePreview } from "../lib/rates";
import { zoneCalendarDate } from "../lib/time-zone";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";
import {
  APPROVER,
  capabilities as capabilitiesOf,
  categories as defaultCategories,
  meta as defaultMeta,
  rates as defaultRates,
  settings as defaultSettings,
  ME,
  noCapabilities,
  ownClaimCapabilities,
  ownDraftCapabilities,
  type StoredExpense,
  stats,
} from "./fixtures";

/** A read the stub answers from a fixture, or a `Response` when the test wants a refusal. */
type Read<T> = T | Response;

export interface ExpensesServer {
  meta?: Read<ExpensesMeta>;
  /**
   * The store the fake keeps expenses in. It is the array the test passes, so
   * a test can read back what a write left behind, and a create shows up in
   * the next list read exactly as it would from the server.
   */
  entries?: StoredExpense[];
  /**
   * The travel claims the fake keeps, as the array the test passes. A claim's
   * `lines`, `lineCount` and `totals` are **derived from the entries store on
   * every read**, so a line recorded against a claim shows up on the trip
   * exactly as it would from the server, and a test never has to keep two
   * stores in step.
   */
  claims?: Claim[];
  /**
   * A refusal for `GET /claims` alone, leaving the store answering every other
   * claim read. A page that lists trips beside expenses has to say when only
   * half of it could be read.
   */
  claimList?: Response;
  /**
   * A refusal for `GET /entries` alone, leaving every other read answering
   * from the store — the one half of a page failing while the other does not.
   */
  entryList?: Response;
  /**
   * Whether `GET /entries?toInvoice=true` is refused, independently of the
   * summary. The refusal follows the summary's own rule by default — the two
   * are the same question — but the panel only offers the chip when the
   * summary was readable, so nothing could otherwise reach it: this is the
   * rights-changed-between-the-two-reads case, and the only way to test the
   * chip's refusal on the very request that carries it.
   */
  refuseToInvoice?: boolean;
  /** What the per diem suggestion answers. Left out, the fake works the days out itself. */
  suggestion?: Read<PerDiemSuggestedDay[]>;
  /**
   * Answers every read of one travel claim with the snapshot the first read
   * produced, however much the store moves afterwards — a page whose refetch
   * has gone stale underneath it, which is what a write's own answer has to
   * survive.
   */
  frozenReads?: boolean;
  projects?: Read<ExpenseProjectOption[]>;
  /**
   * What `GET /projects/{projectId}/summary` answers.
   *
   * Left out, it is **derived from the entries store** under the server's own
   * rules — bucketed by the *unit's* status (a claim's line by its claim, a
   * rejected expense as a draft), per currency and never converted, with
   * ready / invoiced / unpriced pinned per currency and never a per diem day
   * — so a cost recorded or a line marked invoiced in one step moves the
   * figures in the next read exactly as the server would.
   *
   * A `Response` puts a refusal in its place: the module answers **one bare
   * 404** for an installation with no projects, an unknown project and a
   * caller without financial rights alike, which is what the tab must read as
   * "not yours" rather than as a failure.
   *
   * An explicit summary object is how a test models the seam the store cannot:
   * the aggregate and the rows are gated differently, so the totals may cover
   * expenses the list is not allowed to show.
   */
  projectSummary?: Read<ProjectExpensesSummary>;
  /** What the derived summary reports as `capabilities.canRecord`; false by default. */
  canRecord?: boolean;
  /**
   * The project's own currency, as the derived summary reports it. Absent
   * models a project with no currency, where no card is the project's own.
   */
  projectCurrency?: string;
  rates?: Read<ExpenseRate[]>;
  stats?: Read<ExpenseStats>;
  /** The caller, whose expenses `userId=` narrows the list to. */
  me?: string;
  /**
   * The approval queue. Left out, it is derived from the store — the
   * submitted expenses, grouped by owner, longest-waiting first — so an
   * approve in one test shows up in the next read exactly as it would.
   */
  approvals?: Read<ExpenseApprovalGroup[]>;
  /** The payroll list. Left out, it is derived from the store the same way. */
  reimbursements?: Read<ExpenseReimbursementGroup[]>;
  /**
   * What `GET /entries/{id}/billing-lines` answers — the lines of the
   * *expense's own* project, judged by the right to price rather than the
   * right to book. Left out, the entry's own current line and nothing else.
   */
  billingLines?: Read<ExpenseBillingLineOption[]>;
  settings?: Read<ExpenseSettings>;
  /** The category store, mutated in place by the writes. */
  categories?: ExpenseCategory[];
  /** What `export.csv` answers. Left out, a one-line file. */
  csv?: Response | string;
  pageSize?: number;
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
  /** Answers a receipt upload instead of the fake's 201; undefined falls through. */
  upload?: (entryId: number, file: File) => Response | undefined;
  /**
   * Paths whose answer is held back until the stub's `release()` is called —
   * matched against the path, or against the path **with its query** for the
   * entries list, so a test can hold one filter's read while another's
   * answers and see what the page does with the previous one meanwhile —
   * a read that is still in flight while another one has already landed. It
   * is what lets a test put `/meta` *after* the claim it is about, which is
   * the order a cold deep link produces and the order a page that derives a
   * wall clock from the installation's zone has to survive.
   */
  hold?: string[];
}

/** The stubbed fetch, plus the release for whatever `hold` is keeping back. */
export type ExpensesStub = StubbedFetch & { release: () => void };

/** The fixture, or the refusal the test put in its place; a Response is cloned so a refetch reads it again. */
const answer = <T>(read: Read<T> | undefined, fallback: T): Response =>
  read instanceof Response ? read.clone() : jsonResponse(200, read ?? fallback);

/**
 * The paging bounds `validatePageParams` holds every list to, and the default
 * `pageParams` falls back to. 25, not 20: `listDefaultPageSize` in
 * `internal/expenses/entries.go`.
 */
const LIST_DEFAULT_PAGE_SIZE = 25;
const LIST_MAX_PAGE_SIZE = 100;
const LIST_MAX_PAGE = Math.floor(2_147_483_647 / LIST_MAX_PAGE_SIZE);

/**
 * One page of a list, as `apicommon.Pagination` builds it. **`totalPages` is
 * the bare ceiling division, so an empty list is `0`** — not 1. A client that
 * treats it as "at least one page" and clamps to it asks for page 0, which the
 * server refuses; the fake answering 1 here hid exactly that.
 */
const page = <T>(data: T[], pageNumber: number, pageSize: number) => {
  const totalPages = Math.ceil(data.length / pageSize);
  const start = (pageNumber - 1) * pageSize;
  return {
    data: data.slice(start, start + pageSize),
    pagination: {
      page: pageNumber,
      pageSize,
      totalCount: data.length,
      totalPages,
      hasNextPage: pageNumber < totalPages,
      hasPreviousPage: pageNumber > 1 && data.length > 0,
    },
  };
};

const problem = (status: number, title: string, errors?: Record<string, string[]>) =>
  jsonResponse(status, { title, status, ...(errors ? { errors } : {}) });

/**
 * The access layer's one uniform refusal — `apicommon.ForbiddenBody()`, the
 * `AuthErrorResponse` of `openapi/common.yaml`. Every 403 in this API is this
 * body, whatever was asked and why; it is not a `ProblemDetails` and names no
 * field. (The *summary's* 404 is the one that is truly empty.)
 */
const forbidden = () =>
  jsonResponse(403, {
    error: { code: "forbidden", message: "You do not have permission to access this resource." },
  });

/**
 * The paging refusal every list shares, in `validatePageParams`' own words and
 * with its own collect-them-all shape. Returned **before** anything is read:
 * the server validates the query first and asks the directory afterwards.
 */
const pageRefusal = (query: URLSearchParams): Response | undefined => {
  const errs: string[] = [];
  const raw = query.get("page");
  const size = query.get("pageSize");
  if (raw !== null) {
    const value = Number(raw);
    if (value < 1) errs.push(`'page' must be 1 or greater, but was ${value}.`);
    else if (value > LIST_MAX_PAGE) errs.push(`'page' must be at most ${LIST_MAX_PAGE}, but was ${value}.`);
  }
  if (size !== null) {
    const value = Number(size);
    if (value < 1 || value > LIST_MAX_PAGE_SIZE) {
      errs.push(`'pageSize' must be between 1 and ${LIST_MAX_PAGE_SIZE}, but was ${value}.`);
    }
  }
  return errs.length > 0 ? problem(400, "Invalid query parameters", { page: errs }) : undefined;
};

/**
 * What the server would price a saved expense at. An outlay is entered as it
 * stands; a mileage line is priced from the dated rate table, exactly as
 * `lib/rates.ts` previews it — which is why a form's preview and the fake's
 * answer agree, and a test that breaks one breaks the other.
 */
const priced = (
  input: ExpenseInput | ExpenseUpdateInput,
  rates: ExpenseRate[],
  currency: string,
): Partial<Expense> & Pick<Expense, "currency" | "grossAmount" | "netAmount" | "owedToEmployee"> => {
  if (input.kind === "mileage") {
    const preview = mileagePreview(rates, input.entryDate, input.distanceKm ?? 0, input.passengers ?? 0);
    const amount = preview?.amount ?? 0;
    return {
      currency,
      distanceKm: input.distanceKm,
      passengers: input.passengers ?? 0,
      rate: preview?.rate,
      passengerRate: preview?.passengerRate,
      grossAmount: amount,
      netAmount: amount,
      owedToEmployee: amount,
      fromPlace: input.fromPlace,
      toPlace: input.toPlace,
    };
  }
  const gross = input.grossAmount ?? 0;
  const vat = input.vatAmount ?? 0;
  return {
    currency: input.currency ?? currency,
    supplier: input.supplier,
    paidBy: input.paidBy as Expense["paidBy"],
    grossAmount: gross,
    vatAmount: vat === 0 ? undefined : vat,
    netAmount: round2(gross - vat),
    owedToEmployee: input.paidBy === "company" ? 0 : gross,
  };
};

/** The figures a group carries, one line per currency and nothing converted. */
const totalsOf = (entries: Expense[]): ExpenseCurrencyTotal[] => {
  const byCurrency = new Map<string, ExpenseCurrencyTotal>();
  for (const entry of entries) {
    const line = byCurrency.get(entry.currency) ?? { currency: entry.currency, gross: 0, owedToEmployee: 0 };
    byCurrency.set(entry.currency, {
      currency: entry.currency,
      gross: round2(line.gross + entry.grossAmount),
      owedToEmployee: round2(line.owedToEmployee + entry.owedToEmployee),
    });
  }
  return [...byCurrency.values()].sort((a, b) => a.currency.localeCompare(b.currency));
};

/** One person's units in a queue: their loose expenses and their whole trips. */
interface UnitGroup {
  user: Expense["owner"];
  entries: Expense[];
  claims: Claim[];
}

/**
 * How long a group has been waiting: the earliest day any of its units is
 * about — an expense's own date, a trip's departure. The real queue orders by
 * the oldest submission; this is the same approximation the fake made before
 * travel claims existed, widened so that a person whose only unit is a trip
 * takes their place in the order rather than falling to the end.
 */
const waitingSince = (group: UnitGroup): string => {
  const days = [
    ...group.entries.map((entry) => entry.entryDate),
    ...group.claims.map((claim) => claim.departureAt.slice(0, 10)),
  ].sort();
  return days[0] ?? "9999-12-31";
};

/**
 * The two kinds of unit grouped per person, **the one who has been waiting
 * longest first** — a documented property of `GET /approvals`, and one a queue
 * paged by person would be wrong without. A person with nothing but trips is a
 * group of their own: the server counts units, not expenses, and a queue that
 * dropped them would hide the very thing this delivery adds.
 */
const groupedUnits = (entries: Expense[], claims: Claim[]): UnitGroup[] => {
  const byUser = new Map<string, UnitGroup>();
  const groupFor = (user: Expense["owner"]) => {
    const existing = byUser.get(user.userId);
    if (existing) return existing;
    const fresh: UnitGroup = { user, entries: [], claims: [] };
    byUser.set(user.userId, fresh);
    return fresh;
  };
  for (const entry of entries) groupFor(entry.owner).entries.push(entry);
  for (const claim of claims) groupFor(claim.owner).claims.push(claim);
  return [...byUser.values()]
    .map((group) => ({
      ...group,
      entries: [...group.entries].sort((a, b) =>
        a.entryDate === b.entryDate ? a.id - b.id : a.entryDate < b.entryDate ? -1 : 1,
      ),
      claims: [...group.claims].sort((a, b) =>
        a.departureAt === b.departureAt ? a.id - b.id : a.departureAt < b.departureAt ? -1 : 1,
      ),
    }))
    .sort((a, b) => waitingSince(a).localeCompare(waitingSince(b)));
};

/**
 * The Expenses API as the pages read and write it, from an in-memory store.
 * It answers `/meta`, `/entries` (list, read, create, replace, delete),
 * `/submit`, `/approve`, `/reject`, `/unapprove`, the rate override, the
 * billing door and the lines it may pick from, `/approvals`,
 * `/reimbursements` (+ `/reimbursed`, its undo
 * and the CSV), `/settings`, `/rates` (+ reset), `/categories`, `/projects`,
 * `/stats` and the two receipt operations. `write` and `upload` let a test
 * put a refusal in the place of any of them.
 *
 * The queue and the payroll list are **derived from the same store** the
 * writes move, so approving in one step changes what the next read answers,
 * the way the server does.
 *
 * **What it deliberately does not model**, so that nobody reads a green test
 * here as a statement about the server:
 *
 * - **Row-level visibility.** Every caller is answered every entry in the
 *   store; the server's predicate is `see_all ∨ own ∨ a project you manage`.
 *   The gap between a project's *totals* and the *rows* underneath them —
 *   which the project page exists to explain — therefore has to be staged with
 *   an explicit `projectSummary`, never by changing who is asking.
 * - **The derived summary sums the store**, which is the same set the list
 *   answers, for the same reason.
 * - **An invoiced line that is not billable** is counted but adds nothing to
 *   `invoicedAmount`; the server sums every row carrying `invoiced_at`. No
 *   write door can produce one, so the two agree in practice.
 */
export const stubExpensesApi = (server: ExpensesServer = {}): ExpensesStub => {
  const entries = server.entries ?? [];
  let releaseHeld = () => {};
  const held = new Promise<void>((resolve) => {
    releaseHeld = resolve;
  });
  /** A held path answers only once the test says so; everything else answers at once. */
  const maybeHold = (path: string, answer: Promise<Response>): Promise<Response> =>
    server.hold?.includes(path) ? held.then(() => answer) : answer;
  const claims = server.claims ?? [];
  const metaOf = (): ExpensesMeta =>
    server.meta instanceof Response ? defaultMeta() : (server.meta ?? defaultMeta({ categories: defaultCategories }));
  const me = server.me ?? ME;
  let nextId = 9000;
  let nextAttachmentId = 8000;
  const takeId = () => {
    nextId += 1;
    return nextId;
  };
  const takeAttachmentId = () => {
    nextAttachmentId += 1;
    return nextAttachmentId;
  };

  /** The projects the caller may book on, as `GET /projects` answers them. */
  const projectsOf = (): ExpenseProjectOption[] => (server.projects instanceof Response ? [] : (server.projects ?? []));

  const rateStore = server.rates instanceof Response ? [...defaultRates] : (server.rates ?? [...defaultRates]);
  const ratesOf = (): ExpenseRate[] => rateStore;
  const categoryStore = server.categories ?? [...defaultCategories];
  let settingsStore = server.settings instanceof Response ? defaultSettings() : (server.settings ?? defaultSettings());

  const find = (id: number) => entries.find((entry) => entry.id === id);
  const findClaim = (id: number) => claims.find((one) => one.id === id);
  /** The snapshots `frozenReads` answers with, one per claim. */
  const frozenClaims = new Map<number, Claim>();

  /** A claim's lines, oldest day first and then as recorded — the order the server answers. */
  const linesOf = (claimId: number): Expense[] =>
    entries
      .filter((entry) => entry.claimId === claimId)
      .sort((a, b) => (a.entryDate === b.entryDate ? a.id - b.id : a.entryDate < b.entryDate ? -1 : 1));

  /** One claim as `GET /claims/{id}` answers it: the header plus the lines the store holds. */
  const claimResponse = (claim: Claim): Claim => {
    const lines = linesOf(claim.id);
    return { ...claim, lines: renderEntries(lines), totals: totalsOf(lines) };
  };

  /**
   * One claim as a list row: the header, a count instead of the lines, and no
   * billable totals — the fields `ExpensesClaimListResponse` carries, written
   * out so the fake cannot accidentally answer more than the contract does.
   */
  const claimListResponse = (claim: Claim): ClaimListItem => {
    const held = linesOf(claim.id);
    return {
      id: claim.id,
      purpose: claim.purpose,
      ...(claim.destination ? { destination: claim.destination } : {}),
      abroad: claim.abroad,
      ...(claim.abroadDayRate !== undefined ? { abroadDayRate: claim.abroadDayRate } : {}),
      ...(claim.abroadCurrency ? { abroadCurrency: claim.abroadCurrency } : {}),
      departureAt: claim.departureAt,
      returnAt: claim.returnAt,
      ...(claim.project ? { project: claim.project } : {}),
      status: claim.status,
      owner: claim.owner,
      ...(claim.submittedAt ? { submittedAt: claim.submittedAt } : {}),
      ...(claim.decision ? { decision: claim.decision } : {}),
      ...(claim.reimbursement ? { reimbursement: claim.reimbursement } : {}),
      lineCount: held.length,
      totals: totalsOf(held),
      revision: claim.revision,
      createdAt: claim.createdAt,
      updatedAt: claim.updatedAt,
      capabilities: claim.capabilities,
    };
  };

  /**
   * One claim as a *queue unit*: the trip at a glance with the figures whoever
   * is deciding needs, and never its lines — those are one read away at
   * `GET /claims/{id}`. Every count is derived from the entries store, the way
   * the queue's own SQL derives it.
   */
  const claimSummary = (claim: Claim): ClaimSummary => {
    const held = linesOf(claim.id);
    return {
      id: claim.id,
      purpose: claim.purpose,
      ...(claim.destination ? { destination: claim.destination } : {}),
      departureAt: claim.departureAt,
      returnAt: claim.returnAt,
      ...(claim.project ? { project: claim.project } : {}),
      // The trip's own status and its payroll stamp: a queue row says what a
      // trip is rather than inferring it from the list it arrived in.
      status: claim.status,
      ...(claim.reimbursement ? { reimbursement: claim.reimbursement } : {}),
      lineCount: held.length,
      totals: totalsOf(held),
      receiptsMissing: held.filter((one) => one.kind === "outlay" && one.attachmentCount === 0).length,
      overriddenRates: held.filter((one) => one.rateOverride !== undefined).length,
      capabilities: claim.capabilities,
    };
  };

  /**
   * The status the line is judged by: its **claim's** when it is a trip's
   * line, its own otherwise. `COALESCE(claim.status, entry.status)`, in the
   * same words as the SQL every read of this module uses.
   */
  const unitStatusOf = (entry: Expense): Expense["status"] =>
    entry.claimId === undefined ? entry.status : (findClaim(entry.claimId)?.status ?? entry.status);

  /** Which bucket a line falls in. A **rejected** expense counts as a draft: it is back with its owner. */
  const bucketOf = (entry: Expense): "approved" | "submitted" | "draft" =>
    unitStatusOf(entry) === "approved" ? "approved" : unitStatusOf(entry) === "submitted" ? "submitted" : "draft";

  /**
   * The `bill_amount` **column**, which is what every figure below turns on —
   * never `billing`, which is a rendering and is absent from a caller who may
   * not see it. A fixture states it with `billAmount: null` (SQL NULL) or a
   * number; left out, it is the `billing` block's own amount, and NULL when
   * there is no block.
   */
  const billAmountOf = (entry: StoredExpense): number | undefined =>
    entry.billAmount === undefined ? entry.billing?.billAmount : (entry.billAmount ?? undefined);

  /**
   * Ready to invoice, in the words the provider sums it and
   * `GET /entries?toInvoice=true` lists it: the **unit** approved, the line
   * billable, a bill amount present, not invoiced yet, and never a per diem
   * day — which the invoicing door refuses outright.
   */
  const isReady = (entry: StoredExpense): boolean =>
    bucketOf(entry) === "approved" &&
    entry.billable &&
    billAmountOf(entry) !== undefined &&
    entry.billing?.invoice === undefined &&
    entry.kind !== "per_diem";

  /**
   * One entry as it goes on the wire. `billing` is a **rendering**, exactly as
   * `responses.go` makes it: present for a caller who may see it — with
   * `billAmount: 0` where the column is NULL, which is what `floatFromNumeric`
   * does — and absent for one who may not. The column itself never travels.
   */
  const renderEntry = (entry: StoredExpense): Expense => {
    const wire: StoredExpense = { ...entry };
    // The column never travels; `billing` is what the caller is shown.
    delete wire.billAmount;
    // A line's rendered status is its **unit's** — a trip's line is its trip's
    // — which is what `responses.go` puts on the wire and what every badge in
    // this package reads.
    wire.status = unitStatusOf(entry);
    if (!entry.capabilities.canSeeBilling) return { ...wire, billing: undefined };
    return { ...wire, billing: { ...entry.billing, billAmount: billAmountOf(entry) ?? 0 } };
  };

  const renderEntries = (rows: StoredExpense[]): Expense[] => rows.map(renderEntry);

  /**
   * One project's expenses in sum, per currency, by currency code ascending.
   * `total` is carried beside the three buckets because the server rounds each
   * of them once on its own — a client that adds them up is reading a figure
   * nobody published.
   */
  /**
   * Whether this caller is refused the project's figures — the summary's bare
   * 404, and the very same answer `toInvoice=true` is refused under, because
   * what a line bills is the project's money either way. The fake has one
   * caller, so it is expressed as a fixture: an explicit refusal in
   * `projectSummary`, or no projects module at all.
   */
  const summaryRefused = (): boolean => server.projectSummary instanceof Response || !metaOf().projectsAvailable;

  const projectSummaryOf = (projectId: number): ProjectExpensesSummary => {
    // The projects module is what makes this endpoint exist at all: without it
    // the summary is one bare 404, the same one an unknown project gets.
    const mine = entries.filter((entry) => entry.project?.id === projectId);
    const byCurrency = new Map<string, ProjectExpensesCurrency>();
    const empty = (): ProjectExpensesBucket => ({ count: 0, cost: 0, billAmount: 0 });
    for (const entry of mine) {
      const figures =
        byCurrency.get(entry.currency) ??
        ({
          currency: entry.currency,
          approved: empty(),
          submitted: empty(),
          draft: empty(),
          total: empty(),
          readyCount: 0,
          readyAmount: 0,
          invoicedCount: 0,
          invoicedAmount: 0,
          unpricedCount: 0,
        } satisfies ProjectExpensesCurrency);
      const priced = billAmountOf(entry);
      const bills = entry.billable ? (priced ?? 0) : 0;
      for (const bucket of [figures[bucketOf(entry)], figures.total]) {
        bucket.count += 1;
        bucket.cost = round2(bucket.cost + entry.netAmount);
        bucket.billAmount = round2(bucket.billAmount + bills);
      }
      if (isReady(entry)) {
        figures.readyCount += 1;
        figures.readyAmount = round2(figures.readyAmount + bills);
      }
      if (entry.billing?.invoice !== undefined) {
        figures.invoicedCount += 1;
        figures.invoicedAmount = round2(figures.invoicedAmount + bills);
      }
      // A billable line with no bill amount at all: counted, never billed as
      // zero. A per diem day is never billable, so it is never in this figure.
      if (entry.billable && priced === undefined && entry.kind !== "per_diem") figures.unpricedCount += 1;
      byCurrency.set(entry.currency, figures);
    }
    const last = mine
      .map((entry) => entry.entryDate)
      .sort()
      .at(-1);
    return {
      currencies: [...byCurrency.values()].sort((a, b) => a.currency.localeCompare(b.currency)),
      ...(server.projectCurrency ? { projectCurrency: server.projectCurrency } : {}),
      ...(last ? { lastEntryDate: last } : {}),
      capabilities: { canRecord: server.canRecord ?? false },
    };
  };

  /**
   * The period lock, **judged per unit** — the one rule the fake did not model
   * and the one that hid a real bug. A standalone expense is judged on its own
   * `entryDate`; a line of a travel claim is judged on the claim's **departure
   * day** in the installation's zone, because the claim is the unit and a trip
   * that departed after the lock may hold a receipt from before it.
   * `expenses:manage` is never held back.
   */
  const lockRefusal = (date: string, claim: Claim | undefined): Record<string, string[]> | undefined => {
    const meta = metaOf();
    const locked = meta.lockedBefore;
    if (!locked || meta.capabilities.canManage) return undefined;
    const judged = claim ? zoneCalendarDate(claim.departureAt, meta.timeZone) : date;
    if (judged >= locked) return undefined;
    return claim
      ? { claimId: [`Travel claim ${claim.id} departed before ${locked}, the lock date`] }
      : { entryDate: [`An expense dated before ${locked} cannot be recorded`] };
  };

  /** The rate row in force on a date for a kind — the rule `EffectiveRate` applies in SQL. */
  const rateOn = (kind: string, date: string): ExpenseRate | undefined =>
    ratesOf()
      .filter((rate) => rate.kind === kind && rate.validFrom <= date)
      .sort((a, b) => (a.validFrom < b.validFrom ? -1 : 1))
      .at(-1);

  const perDiemRateKind = (type: PerDiemType): string => `per_diem_${type.replace(/^day_/, "")}`;

  /**
   * What the server prices a per diem day at: the day rate in force on its
   * own date for its own type — or the claim's own rate abroad — less each
   * covered meal's percentage, rounded once and never below zero. A rate that
   * is not there refuses, on the field that named it, exactly as the module
   * does.
   */
  const pricePerDiem = (
    input: ExpenseInput | ExpenseUpdateInput,
    claim: Claim,
    defaultCurrency: string,
  ): { line: Partial<Expense> & { perDiem: PerDiem }; refusal?: Record<string, string[]> } => {
    const type = (input.perDiemType ?? "") as PerDiemType;
    const date = input.entryDate;
    const kind = perDiemRateKind(type);
    const dayRate = claim.abroad ? claim.abroadDayRate : rateOn(kind, date)?.value;
    const covered = {
      breakfast: input.breakfastCovered ?? false,
      lunch: input.lunchCovered ?? false,
      dinner: input.dinnerCovered ?? false,
    };
    const errors: Record<string, string[]> = {};
    if (dayRate === undefined) errors.perDiemType = [`No ${kind} rate applies on ${date}`];
    const percents: PerDiem["mealPercents"] = {};
    for (const meal of meals) {
      const percent = rateOn(`meal_${meal}_percent`, date)?.value;
      if (percent !== undefined) percents[meal] = percent;
      else if (covered[meal]) errors[`${meal}Covered`] = [`No meal_${meal}_percent rate applies on ${date}`];
    }
    const deducted = meals.reduce((sum, meal) => sum + (covered[meal] ? (percents[meal] ?? 0) : 0), 0);
    const amount = Math.max(0, round2((dayRate ?? 0) * (1 - deducted / 100)));
    return {
      line: {
        currency: claim.abroad ? (claim.abroadCurrency ?? defaultCurrency) : defaultCurrency,
        rate: dayRate,
        grossAmount: amount,
        netAmount: amount,
        owedToEmployee: amount,
        perDiem: {
          type,
          breakfastCovered: covered.breakfast,
          lunchCovered: covered.lunch,
          dinnerCovered: covered.dinner,
          dayRate: dayRate ?? 0,
          mealPercents: percents,
        },
      },
      ...(Object.keys(errors).length > 0 ? { refusal: errors } : {}),
    };
  };

  /** The days the server would propose, priced from the table and marked where one exists. */
  const suggestDays = (claim: Claim, overnight: boolean): PerDiemSuggestedDay[] => {
    const zone = metaOf().timeZone;
    const departure = new Date(claim.departureAt).getTime();
    const duration = new Date(claim.returnAt).getTime() - departure;
    const period = 24 * 3_600_000;
    const part = 6 * 3_600_000;
    if (duration < part) return [];
    const starts: { at: number; type: PerDiemType }[] = [];
    if (!overnight) {
      starts.push({ at: departure, type: duration <= 12 * 3_600_000 ? "day_6_12" : "day_over_12" });
    } else {
      const whole = Math.floor(duration / period);
      const count = whole === 0 ? 1 : duration % period > part ? whole + 1 : whole;
      for (let index = 0; index < count; index += 1) {
        starts.push({ at: departure + index * period, type: "overnight_hotel" });
      }
    }
    const taken = new Set(
      linesOf(claim.id)
        .filter((line) => line.kind === "per_diem")
        .map((line) => line.entryDate),
    );
    return starts.map(({ at, type }) => {
      const entryDate = zoneCalendarDate(new Date(at).toISOString(), zone);
      const dayRate = claim.abroad ? claim.abroadDayRate : rateOn(perDiemRateKind(type), entryDate)?.value;
      // The currency travels with the figures — the claim's own abroad, the
      // installation's default otherwise — and is absent with them, so a
      // client never has to infer it from the claim.
      const currency = claim.abroad ? (claim.abroadCurrency ?? metaOf().defaultCurrency) : metaOf().defaultCurrency;
      return {
        entryDate,
        perDiemType: type,
        exists: taken.has(entryDate),
        ...(dayRate === undefined ? {} : { dayRate, amount: dayRate, currency }),
      };
    });
  };

  /** Every batch refuses as one, with a message per offending id on the list that named it. */
  const missing = (title: string, ids: number[], claimIds: number[]): Response | undefined => {
    const unknownEntries = ids.filter((id) => !find(id));
    const unknownClaims = claimIds.filter((id) => !findClaim(id));
    if (unknownEntries.length === 0 && unknownClaims.length === 0) return undefined;
    return problem(400, title, {
      ...(unknownEntries.length > 0 ? { entryIds: unknownEntries.map((id) => `Expense ${id} was not found`) } : {}),
      ...(unknownClaims.length > 0 ? { claimIds: unknownClaims.map((id) => `Travel claim ${id} was not found`) } : {}),
    });
  };

  /**
   * What a batch answers: `{ entries, claims }`, each in the order its own ids
   * were given. A trip moves as one unit — its lines take the claim's status
   * without being named — which is why the two lists are separate and why a
   * caller reads `moved.entries` rather than the array the operations used to
   * answer before travel claims existed.
   */
  const moveAll = (
    ids: number[],
    claimIds: number[],
    move: (entry: Expense) => void,
    moveClaim?: (claim: Claim) => void,
  ): Response => {
    const movedEntries = ids.map((id) => {
      const entry = find(id) as StoredExpense;
      move(entry);
      entry.revision += 1;
      return renderEntry(entry);
    });
    const movedClaims = claimIds.map((id) => {
      const claim = findClaim(id) as Claim;
      moveClaim?.(claim);
      claim.revision += 1;
      // A line's rendered status is its claim's, so the trip's lines follow it.
      for (const line of linesOf(claim.id)) line.status = claim.status;
      return claimListResponse(claim);
    });
    return jsonResponse(200, { entries: movedEntries, claims: movedClaims });
  };

  const stub = stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    const isForm = typeof FormData !== "undefined" && init?.body instanceof FormData;
    const body = init?.body && !isForm ? JSON.parse(String(init.body)) : undefined;
    const answered = method === "GET" ? undefined : server.write?.(method, path, body);
    if (answered) return Promise.resolve(answered);

    if (path === "/api/v1/expenses/meta")
      return maybeHold(path, Promise.resolve(answer(server.meta, defaultMeta({ categories: categoryStore }))));
    if (path === "/api/v1/expenses/projects") return Promise.resolve(answer(server.projects, []));

    const projectSummary = /^\/api\/v1\/expenses\/projects\/(\d+)\/summary$/.exec(path);
    if (projectSummary && method === "GET") {
      // One bare 404 with an empty body, exactly as the module answers it: no
      // projects module, no such project and no financial rights are
      // deliberately indistinguishable.
      if (server.projectSummary instanceof Response) return Promise.resolve(server.projectSummary.clone());
      // Without the projects module the endpoint is one of the three things
      // that bare 404 means, and the derived answer must say so too — a test
      // that passes because the panel short-circuits first is a test about the
      // panel, not about the server.
      if (summaryRefused()) return Promise.resolve(new Response(null, { status: 404 }));
      return Promise.resolve(jsonResponse(200, server.projectSummary ?? projectSummaryOf(Number(projectSummary[1]))));
    }
    if (path === "/api/v1/expenses/stats") return Promise.resolve(answer(server.stats, stats()));

    if (path === "/api/v1/expenses/rates") {
      if (server.rates instanceof Response && method === "GET") return Promise.resolve(server.rates.clone());
      if (method === "GET") return Promise.resolve(jsonResponse(200, rateStore));
      if (method === "POST") {
        const saved: ExpenseRate = { id: takeId(), ...body };
        rateStore.push(saved);
        return Promise.resolve(jsonResponse(201, saved));
      }
    }
    if (path === "/api/v1/expenses/rates/reset" && method === "POST") {
      for (const shipped of defaultRates) {
        const row = rateStore.find((one) => one.kind === body.kind && one.validFrom === shipped.validFrom);
        if (shipped.kind !== body.kind) continue;
        if (row) Object.assign(row, shipped);
        else rateStore.push({ ...shipped });
      }
      return Promise.resolve(jsonResponse(200, rateStore));
    }
    const rateRow = /^\/api\/v1\/expenses\/rates\/(\d+)$/.exec(path);
    if (rateRow) {
      const id = Number(rateRow[1]);
      const index = rateStore.findIndex((one) => one.id === id);
      if (index === -1) return Promise.resolve(new Response(null, { status: 404 }));
      if (method === "DELETE") {
        rateStore.splice(index, 1);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (method === "PUT") {
        rateStore[index] = { ...rateStore[index], ...body, currency: body.currency };
        return Promise.resolve(jsonResponse(200, rateStore[index]));
      }
    }

    if (path === "/api/v1/expenses/settings") {
      if (method === "GET") return Promise.resolve(answer(server.settings, settingsStore));
      if (method === "PUT") {
        settingsStore = { ...body };
        return Promise.resolve(jsonResponse(200, settingsStore));
      }
    }

    if (path === "/api/v1/expenses/categories") {
      if (method === "GET") return Promise.resolve(jsonResponse(200, categoryStore));
      if (method === "POST") {
        const saved: ExpenseCategory = {
          id: takeId(),
          name: body.name,
          active: body.active ?? true,
          position: body.position ?? categoryStore.length + 1,
        };
        categoryStore.push(saved);
        return Promise.resolve(jsonResponse(201, saved));
      }
    }
    const categoryRow = /^\/api\/v1\/expenses\/categories\/(\d+)$/.exec(path);
    if (categoryRow && method === "PUT") {
      const id = Number(categoryRow[1]);
      const row = categoryStore.find((one) => one.id === id);
      if (!row) return Promise.resolve(new Response(null, { status: 404 }));
      Object.assign(row, body);
      categoryStore.sort((a, b) =>
        a.position === b.position ? a.name.localeCompare(b.name) : a.position - b.position,
      );
      return Promise.resolve(jsonResponse(200, row));
    }

    if (path === "/api/v1/expenses/approvals" && method === "GET") {
      const badPaging = pageRefusal(url.searchParams);
      if (badPaging) return Promise.resolve(badPaging);
      if (server.approvals instanceof Response) return Promise.resolve(server.approvals.clone());
      // A trip's lines are never loose entries in a queue: the claim is the
      // unit, and its lines take their rendered status from it.
      const groups: ExpenseApprovalGroup[] =
        server.approvals ??
        groupedUnits(
          entries.filter((entry) => entry.status === "submitted" && entry.claimId === undefined),
          claims.filter((claim) => claim.status === "submitted"),
        ).map((group) => {
          // The group's figures hold both kinds of unit — how much of this
          // person's work there is to look at — so the client does no
          // arithmetic of its own.
          const lines = [...group.entries, ...group.claims.flatMap((claim) => linesOf(claim.id))];
          return {
            user: group.user,
            entries: renderEntries(group.entries),
            claims: group.claims.map(claimSummary),
            totals: totalsOf(lines),
            receiptsMissing: lines.filter((one) => one.kind === "outlay" && one.attachmentCount === 0).length,
            overriddenRates: lines.filter((one) => one.rateOverride !== undefined).length,
          };
        });
      return Promise.resolve(
        jsonResponse(
          200,
          page(groups, Number(url.searchParams.get("page") ?? 1), server.pageSize ?? LIST_DEFAULT_PAGE_SIZE),
        ),
      );
    }

    if (path === "/api/v1/expenses/approve" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const claimIds: number[] = body?.claimIds ?? [];
      const refusal = missing("Invalid approval", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const decide = (unit: Expense | Claim) => {
        unit.status = "approved";
        unit.decision = { status: "approved", at: "2026-09-20T09:00:00Z", by: APPROVER };
        unit.capabilities = { ...unit.capabilities, canApprove: false, canUnapprove: true };
      };
      return Promise.resolve(moveAll(ids, claimIds, decide, decide));
    }
    if (path === "/api/v1/expenses/reject" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const claimIds: number[] = body?.claimIds ?? [];
      const refusal = missing("Invalid approval", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const decide = (unit: Expense | Claim) => {
        unit.status = "rejected";
        unit.decision = { status: "rejected", at: "2026-09-20T09:00:00Z", by: APPROVER, reason: body.reason };
        unit.capabilities = { ...unit.capabilities, canApprove: false, canEdit: true, canSubmit: true };
      };
      return Promise.resolve(moveAll(ids, claimIds, decide, decide));
    }
    if (path === "/api/v1/expenses/unapprove" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const claimIds: number[] = body?.claimIds ?? [];
      const refusal = missing("Invalid approval", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const undo = (unit: Expense | Claim) => {
        unit.status = "draft";
        unit.decision = undefined;
        unit.submittedAt = undefined;
        unit.capabilities = { ...unit.capabilities, canUnapprove: false, canEdit: true, canSubmit: true };
      };
      return Promise.resolve(
        moveAll(
          ids,
          claimIds,
          (entry) => {
            undo(entry);
            entry.rateOverride = undefined;
          },
          undo,
        ),
      );
    }

    const rateOverride = /^\/api\/v1\/expenses\/entries\/(\d+)\/rate$/.exec(path);
    if (rateOverride && method === "PUT") {
      const entry = find(Number(rateOverride[1]));
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      if (body.revision !== entry.revision) {
        return Promise.resolve(jsonResponse(409, { title: "The expense has moved on", status: 409 }));
      }
      entry.rateOverride = {
        byUser: APPROVER,
        ...(entry.rateOverride?.tableValue !== undefined
          ? { tableValue: entry.rateOverride.tableValue }
          : entry.rate !== undefined
            ? { tableValue: entry.rate }
            : {}),
        ...(body.passengerRate !== undefined && entry.passengerRate !== undefined
          ? { passengerTableValue: entry.rateOverride?.passengerTableValue ?? entry.passengerRate }
          : {}),
      };
      entry.rate = body.rate;
      if (body.passengerRate !== undefined) entry.passengerRate = body.passengerRate;
      // A per diem day is repriced from the new day rate and **the meal
      // percentages the line was saved with** — never the table as it stands
      // today — so correcting a rate neither drops a breakfast somebody else
      // paid for nor picks up a percentage that has changed since.
      let amount: number;
      if (entry.kind === "per_diem" && entry.perDiem) {
        const held = entry.perDiem;
        const deducted =
          (held.breakfastCovered ? (held.mealPercents.breakfast ?? 0) : 0) +
          (held.lunchCovered ? (held.mealPercents.lunch ?? 0) : 0) +
          (held.dinnerCovered ? (held.mealPercents.dinner ?? 0) : 0);
        amount = Math.max(0, round2(body.rate * (1 - deducted / 100)));
        entry.perDiem = { ...held, dayRate: body.rate };
      } else {
        amount = round2(
          (entry.distanceKm ?? 0) * body.rate +
            (entry.distanceKm ?? 0) * (entry.passengerRate ?? 0) * (entry.passengers ?? 0),
        );
      }
      entry.grossAmount = amount;
      entry.netAmount = amount;
      entry.owedToEmployee = amount;
      entry.revision += 1;
      return Promise.resolve(jsonResponse(200, renderEntry(entry)));
    }

    const billingLines = /^\/api\/v1\/expenses\/entries\/(\d+)\/billing-lines$/.exec(path);
    if (billingLines && method === "GET") {
      if (server.billingLines instanceof Response) return Promise.resolve(server.billingLines.clone());
      const entry = find(Number(billingLines[1]));
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      const own: ExpenseBillingLineOption[] = entry.billingLine
        ? [{ id: entry.billingLine.id, code: entry.billingLine.code, active: true }]
        : [];
      return Promise.resolve(jsonResponse(200, server.billingLines ?? own));
    }

    const invoiced = /^\/api\/v1\/expenses\/entries\/(\d+)\/invoiced$/.exec(path);
    if (invoiced && method === "POST") {
      const entry = find(Number(invoiced[1]));
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      if (body.revision !== entry.revision) {
        return Promise.resolve(jsonResponse(409, { title: "The expense has moved on", status: 409 }));
      }
      entry.billing = {
        billAmount: entry.billing?.billAmount ?? 0,
        ...(entry.billing ?? {}),
        invoice: { at: "2026-09-20T11:00:00Z", by: APPROVER, ...(body.reference ? { reference: body.reference } : {}) },
      };
      entry.capabilities = { ...entry.capabilities, canMarkInvoiced: false, canUndoInvoiced: true };
      entry.revision += 1;
      return Promise.resolve(jsonResponse(200, renderEntry(entry)));
    }

    const invoicedUndo = /^\/api\/v1\/expenses\/entries\/(\d+)\/invoiced\/undo$/.exec(path);
    if (invoicedUndo && method === "POST") {
      const entry = find(Number(invoicedUndo[1]));
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      if (body.revision !== entry.revision) {
        return Promise.resolve(jsonResponse(409, { title: "The expense has moved on", status: 409 }));
      }
      entry.billing = entry.billing ? { ...entry.billing, invoice: undefined } : undefined;
      entry.capabilities = { ...entry.capabilities, canMarkInvoiced: true, canUndoInvoiced: false };
      entry.revision += 1;
      return Promise.resolve(jsonResponse(200, renderEntry(entry)));
    }

    const billing = /^\/api\/v1\/expenses\/entries\/(\d+)\/billing$/.exec(path);
    if (billing && method === "PUT") {
      const entry = find(Number(billing[1]));
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      if (body.revision !== entry.revision) {
        return Promise.resolve(jsonResponse(409, { title: "The expense has moved on", status: 409 }));
      }
      entry.billable = body.billable;
      entry.billingLine = body.billingLineId
        ? entry.billingLine?.id === body.billingLineId
          ? entry.billingLine
          : { id: body.billingLineId, code: "PM" }
        : undefined;
      const markup = body.markupPercent ?? entry.billing?.markupPercent;
      const perKm = body.billRatePerKm ?? entry.billing?.billRatePerKm;
      entry.billing = body.billable
        ? {
            billAmount:
              entry.kind === "mileage"
                ? round2((entry.distanceKm ?? 0) * (perKm ?? 0))
                : round2(entry.netAmount * (1 + (markup ?? 0) / 100)),
            ...(entry.kind === "mileage" ? { billRatePerKm: perKm } : { markupPercent: markup }),
          }
        : { billAmount: 0 };
      entry.revision += 1;
      return Promise.resolve(jsonResponse(200, renderEntry(entry)));
    }

    if (path === "/api/v1/expenses/reimbursements" && method === "GET") {
      const badPaging = pageRefusal(url.searchParams);
      if (badPaging) return Promise.resolve(badPaging);
      if (server.reimbursements instanceof Response) return Promise.resolve(server.reimbursements.clone());
      const state = url.searchParams.get("state") ?? "waiting";
      const owes = (unit: { status?: string; reimbursement?: unknown }, owed: number) =>
        state === "reimbursed"
          ? unit.reimbursement !== undefined
          : unit.status === "approved" && owed > 0 && unit.reimbursement === undefined;
      const owedEntries = entries.filter((entry) => entry.claimId === undefined && owes(entry, entry.owedToEmployee));
      const owedClaims = claims.filter((claim) =>
        owes(
          claim,
          linesOf(claim.id).reduce((sum, line) => sum + line.owedToEmployee, 0),
        ),
      );
      const groups: ExpenseReimbursementGroup[] =
        server.reimbursements ??
        groupedUnits(owedEntries, owedClaims).map((group) => ({
          user: group.user,
          entries: renderEntries(group.entries),
          claims: group.claims.map(claimSummary),
          totals: totalsOf([...group.entries, ...group.claims.flatMap((claim) => linesOf(claim.id))]),
        }));
      return Promise.resolve(
        jsonResponse(
          200,
          page(groups, Number(url.searchParams.get("page") ?? 1), server.pageSize ?? LIST_DEFAULT_PAGE_SIZE),
        ),
      );
    }

    if (path === "/api/v1/expenses/reimbursements/export.csv" && method === "GET") {
      if (server.csv instanceof Response) return Promise.resolve(server.csv.clone());
      return Promise.resolve(
        new Response(server.csv ?? "Employee;Date\r\nAda Lovelace;2026-09-18\r\n", {
          status: 200,
          headers: {
            "Content-Type": "text/csv; charset=utf-8",
            "Content-Disposition": 'attachment; filename="expenses-reimbursements-2026-09-20.csv"',
          },
        }),
      );
    }

    if (path === "/api/v1/expenses/reimbursed" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const claimIds: number[] = body?.claimIds ?? [];
      const refusal = missing("Invalid reimbursement", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const pay = (unit: Expense | Claim) => {
        unit.reimbursement = {
          at: "2026-09-20T10:00:00Z",
          by: APPROVER,
          date: body.date,
          ...(body.reference ? { reference: body.reference } : {}),
        };
        unit.capabilities = { ...unit.capabilities, canMarkReimbursed: false, canUndoReimbursed: true };
      };
      return Promise.resolve(moveAll(ids, claimIds, pay, pay));
    }
    if (path === "/api/v1/expenses/reimbursed/undo" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const claimIds: number[] = body?.claimIds ?? [];
      const refusal = missing("Invalid reimbursement", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const undo = (unit: Expense | Claim) => {
        unit.reimbursement = undefined;
        unit.capabilities = { ...unit.capabilities, canMarkReimbursed: true, canUndoReimbursed: false };
      };
      return Promise.resolve(moveAll(ids, claimIds, undo, undo));
    }

    const upload = /^\/api\/v1\/expenses\/entries\/(\d+)\/attachments$/.exec(path);
    if (upload && method === "POST") {
      const entryId = Number(upload[1]);
      const form = init?.body as FormData | undefined;
      const file = form?.get("file") as File;
      const refusal = server.upload?.(entryId, file);
      if (refusal) return Promise.resolve(refusal);
      const entry = find(entryId);
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      const saved: ExpenseAttachment = {
        id: takeAttachmentId(),
        fileName: file.name,
        contentType: file.type || "application/pdf",
        sizeBytes: file.size,
      };
      entry.attachments = [...entry.attachments, saved];
      entry.attachmentCount = entry.attachments.length;
      return Promise.resolve(jsonResponse(201, saved));
    }

    const receipt = /^\/api\/v1\/expenses\/attachments\/(\d+)$/.exec(path);
    if (receipt && method === "DELETE") {
      const id = Number(receipt[1]);
      for (const entry of entries) {
        entry.attachments = entry.attachments.filter((one) => one.id !== id);
        entry.attachmentCount = entry.attachments.length;
      }
      return Promise.resolve(new Response(null, { status: 204 }));
    }

    if (path === "/api/v1/expenses/submit" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const claimIds: number[] = body?.claimIds ?? [];
      const refusal = missing("Invalid submission", ids, claimIds);
      if (refusal) return Promise.resolve(refusal);
      const send = (unit: Expense | Claim) => {
        unit.status = "submitted";
        unit.submittedAt = "2026-09-19T10:00:00Z";
        unit.decision = undefined;
        unit.capabilities = { ...unit.capabilities, canEdit: false, canDelete: false, canSubmit: false };
      };
      return Promise.resolve(moveAll(ids, claimIds, send, send));
    }

    const suggestion = /^\/api\/v1\/expenses\/claims\/(\d+)\/per-diem-suggestion$/.exec(path);
    if (suggestion && method === "POST") {
      if (server.suggestion instanceof Response) return Promise.resolve(server.suggestion.clone());
      const claim = findClaim(Number(suggestion[1]));
      if (!claim) return Promise.resolve(new Response(null, { status: 404 }));
      return Promise.resolve(jsonResponse(200, server.suggestion ?? suggestDays(claim, Boolean(body?.overnight))));
    }

    const claimRow = /^\/api\/v1\/expenses\/claims\/(\d+)$/.exec(path);
    if (claimRow) {
      const claim = findClaim(Number(claimRow[1]));
      if (!claim) return Promise.resolve(new Response(null, { status: 404 }));
      if (method === "GET") {
        if (!server.frozenReads) return Promise.resolve(jsonResponse(200, claimResponse(claim)));
        // A deep copy: the store's own line objects are mutated in place by
        // the writes, so a shallow snapshot would move with them.
        const frozen = frozenClaims.get(claim.id) ?? (JSON.parse(JSON.stringify(claimResponse(claim))) as Claim);
        frozenClaims.set(claim.id, frozen);
        return Promise.resolve(jsonResponse(200, frozen));
      }
      if (method === "DELETE") {
        for (const line of linesOf(claim.id)) entries.splice(entries.indexOf(line), 1);
        claims.splice(claims.indexOf(claim), 1);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (method === "PUT") {
        if (body.revision !== claim.revision) {
          return Promise.resolve(jsonResponse(409, { title: "The travel claim has moved on", status: 409 }));
        }
        const zone = metaOf().timeZone;
        const after = { ...claim, ...body } as Claim;
        // A narrowed trip refuses rather than strand a day somebody recorded.
        const stranded: Record<string, string[]> = {};
        const from = zoneCalendarDate(after.departureAt, zone);
        const to = zoneCalendarDate(after.returnAt, zone);
        for (const line of linesOf(claim.id).filter((one) => one.kind === "per_diem")) {
          if (line.entryDate < from) {
            stranded.departureAt = [
              `Travel claim ${claim.id} holds a per diem day on ${line.entryDate}, which the trip would no longer cover; remove it first`,
            ];
          }
          if (line.entryDate > to) {
            stranded.returnAt = [
              `Travel claim ${claim.id} holds a per diem day on ${line.entryDate}, which the trip would no longer cover; remove it first`,
            ];
          }
        }
        if (Object.keys(stranded).length > 0) {
          return Promise.resolve(problem(400, "Invalid travel claim", stranded));
        }
        // The claim's project is every line's: a change re-points them in the
        // same transaction, and clearing it makes every line non-billable.
        // (The real server reprices per diem days on a project change too; the
        // fake only reprices on the money, which is what the pages read.)
        const projectAfter =
          body.projectId === undefined
            ? undefined
            : (projectsOf().find((one) => one.id === body.projectId) ??
              claim.project ?? { id: body.projectId, code: "KVEM1000", name: "Kverneland web" });
        const repriced =
          claim.abroad !== after.abroad ||
          claim.abroadDayRate !== after.abroadDayRate ||
          claim.abroadCurrency !== after.abroadCurrency;
        const rePointed = (claim.project?.id ?? undefined) !== (projectAfter?.id ?? undefined);
        // The whole edit is refused when a day cannot be priced after the
        // change — a trip turned domestic on a date the table prices no day of
        // that type — because half a claim repriced is worse than an edit the
        // caller can undo. Worked out **before** anything is written.
        if (repriced) {
          for (const line of linesOf(claim.id).filter((one) => one.kind === "per_diem" && one.perDiem)) {
            const perDiem = line.perDiem as PerDiem;
            const { refusal } = pricePerDiem(
              {
                kind: "per_diem",
                entryDate: line.entryDate,
                perDiemType: perDiem.type,
                breakfastCovered: perDiem.breakfastCovered,
                lunchCovered: perDiem.lunchCovered,
                dinnerCovered: perDiem.dinnerCovered,
              },
              after,
              metaOf().defaultCurrency,
            );
            if (refusal) return Promise.resolve(problem(400, "Invalid travel claim", refusal));
          }
        }
        Object.assign(claim, {
          purpose: after.purpose,
          destination: after.destination,
          abroad: after.abroad ?? false,
          abroadDayRate: after.abroadDayRate,
          abroadCurrency: after.abroadCurrency,
          departureAt: after.departureAt,
          returnAt: after.returnAt,
          project: projectAfter,
          revision: claim.revision + 1,
        });
        // A line's revision moves when the line does: a re-point, or a
        // repricing. An unrelated header edit — a new purpose, a wider window
        // — leaves every line exactly as it was, which is what the server
        // does and what a row holding its own revision depends on.
        if (rePointed) {
          for (const line of linesOf(claim.id)) {
            Object.assign(line, {
              project: projectAfter,
              ...(projectAfter === undefined ? { billable: false, billingLine: undefined, billing: undefined } : {}),
              revision: line.revision + 1,
            });
          }
        }
        if (repriced) {
          // The claim's own money changing reprices every day it holds, in the
          // same transaction — which is why the page reads the claim again.
          for (const line of linesOf(claim.id).filter((one) => one.kind === "per_diem" && one.perDiem)) {
            const perDiem = line.perDiem as PerDiem;
            const { line: priced } = pricePerDiem(
              {
                kind: "per_diem",
                entryDate: line.entryDate,
                perDiemType: perDiem.type,
                breakfastCovered: perDiem.breakfastCovered,
                lunchCovered: perDiem.lunchCovered,
                dinnerCovered: perDiem.dinnerCovered,
              },
              claim,
              metaOf().defaultCurrency,
            );
            Object.assign(line, priced, { revision: line.revision + 1 });
          }
        }
        return Promise.resolve(jsonResponse(200, claimResponse(claim)));
      }
    }

    if (path === "/api/v1/expenses/claims" && method === "POST") {
      const saved: Claim = {
        id: takeId(),
        purpose: body.purpose,
        ...(body.destination ? { destination: body.destination } : {}),
        abroad: body.abroad ?? false,
        ...(body.abroadDayRate !== undefined ? { abroadDayRate: body.abroadDayRate } : {}),
        ...(body.abroadCurrency ? { abroadCurrency: body.abroadCurrency } : {}),
        departureAt: body.departureAt,
        returnAt: body.returnAt,
        ...(body.projectId ? { project: { id: body.projectId, code: "KVEM1000", name: "Kverneland web" } } : {}),
        status: "draft",
        owner: { userId: me, displayName: "Ada Lovelace", active: true },
        lines: [],
        totals: [],
        revision: 1,
        createdAt: "2026-03-08T09:00:00Z",
        updatedAt: "2026-03-08T09:00:00Z",
        capabilities: ownClaimCapabilities,
      };
      claims.push(saved);
      return Promise.resolve(jsonResponse(201, saved));
    }

    if (path === "/api/v1/expenses/claims" && method === "GET") {
      const badPaging = pageRefusal(url.searchParams);
      if (badPaging) return Promise.resolve(badPaging);
      if (server.claimList) return Promise.resolve(server.claimList.clone());
      const query = url.searchParams;
      const userId = query.get("userId");
      const from = query.get("from");
      const to = query.get("to");
      const reimbursed = query.get("reimbursed");
      const zone = metaOf().timeZone;
      const matching = claims
        .filter((claim) => !userId || claim.owner.userId === userId)
        .filter((claim) => !query.get("status") || claim.status === query.get("status"))
        .filter((claim) => !from || zoneCalendarDate(claim.departureAt, zone) >= from)
        .filter((claim) => !to || zoneCalendarDate(claim.departureAt, zone) <= to)
        .filter((claim) => reimbursed === null || (claim.reimbursement !== undefined) === (reimbursed === "true"))
        .sort((a, b) => (a.departureAt === b.departureAt ? b.id - a.id : a.departureAt < b.departureAt ? 1 : -1))
        .map(claimListResponse);
      return Promise.resolve(
        jsonResponse(200, page(matching, Number(query.get("page") ?? 1), server.pageSize ?? LIST_DEFAULT_PAGE_SIZE)),
      );
    }

    const one = /^\/api\/v1\/expenses\/entries\/(\d+)$/.exec(path);
    if (one) {
      const id = Number(one[1]);
      const entry = find(id);
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      if (method === "GET") return Promise.resolve(jsonResponse(200, renderEntry(entry)));
      if (method === "DELETE") {
        entries.splice(entries.indexOf(entry), 1);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (method === "PUT") {
        const update = body as ExpenseUpdateInput;
        if (update.revision !== entry.revision) {
          return Promise.resolve(jsonResponse(409, { title: "The expense has moved on", status: 409 }));
        }
        const claim = entry.claimId === undefined ? undefined : findClaim(entry.claimId);
        const lockedEdit = lockRefusal(update.entryDate, claim);
        if (lockedEdit) return Promise.resolve(problem(400, "Invalid expense", lockedEdit));
        if (update.kind === "per_diem") {
          if (!claim)
            return Promise.resolve(
              problem(400, "Invalid expense", { claimId: ["A per diem belongs to a travel claim"] }),
            );
          // A day must fall inside the trip on a replace as well as on a
          // create; the server checks both, and the fake used to check only
          // the create.
          const zone = metaOf().timeZone;
          const from = zoneCalendarDate(claim.departureAt, zone);
          const to = zoneCalendarDate(claim.returnAt, zone);
          if (update.entryDate < from || update.entryDate > to) {
            return Promise.resolve(
              problem(400, "Invalid expense", { entryDate: [`A per diem day falls between ${from} and ${to}`] }),
            );
          }
          const clash = linesOf(claim.id).find(
            (line) => line.kind === "per_diem" && line.id !== entry.id && line.entryDate === update.entryDate,
          );
          if (clash) {
            return Promise.resolve(
              problem(400, "Invalid expense", {
                entryDate: [`Travel claim ${claim.id} already holds a per diem day on ${update.entryDate}`],
              }),
            );
          }
          const { line, refusal } = pricePerDiem(update, claim, metaOf().defaultCurrency);
          if (refusal) return Promise.resolve(problem(400, "Invalid expense", refusal));
          Object.assign(entry, {
            entryDate: update.entryDate,
            description: "",
            revision: entry.revision + 1,
            ...line,
          });
          return Promise.resolve(jsonResponse(200, renderEntry(entry)));
        }
        Object.assign(entry, {
          kind: update.kind,
          entryDate: update.entryDate,
          description: update.description,
          category: update.categoryId ? categoryStore.find((one) => one.id === update.categoryId) : undefined,
          billable: update.billable ?? false,
          revision: entry.revision + 1,
          supplier: undefined,
          paidBy: undefined,
          vatAmount: undefined,
          distanceKm: undefined,
          passengers: undefined,
          rate: undefined,
          fromPlace: undefined,
          toPlace: undefined,
          ...priced(update, ratesOf(), metaOf().defaultCurrency),
        });
        return Promise.resolve(jsonResponse(200, renderEntry(entry)));
      }
    }

    if (path === "/api/v1/expenses/entries" && method === "POST") {
      const input = body as ExpenseInput;
      const claim = input.claimId === undefined ? undefined : findClaim(input.claimId);
      if (input.claimId !== undefined && !claim) {
        return Promise.resolve(
          problem(400, "Invalid expense", { claimId: [`Travel claim ${input.claimId} was not found`] }),
        );
      }
      const locked = lockRefusal(input.entryDate, claim);
      if (locked) return Promise.resolve(problem(400, "Invalid expense", locked));
      if (claim && linesOf(claim.id).length >= 200) {
        return Promise.resolve(
          problem(400, "Invalid expense", { claimId: ["A travel claim holds at most 200 expenses"] }),
        );
      }
      let perDiem: Partial<Expense> | undefined;
      if (input.kind === "per_diem") {
        if (!claim) {
          return Promise.resolve(problem(400, "Invalid expense", { kind: ["A per diem belongs to a travel claim"] }));
        }
        const zone = metaOf().timeZone;
        const from = zoneCalendarDate(claim.departureAt, zone);
        const to = zoneCalendarDate(claim.returnAt, zone);
        if (input.entryDate < from || input.entryDate > to) {
          return Promise.resolve(
            problem(400, "Invalid expense", {
              entryDate: [`A per diem day falls between ${from} and ${to}`],
            }),
          );
        }
        if (linesOf(claim.id).some((line) => line.kind === "per_diem" && line.entryDate === input.entryDate)) {
          return Promise.resolve(
            problem(400, "Invalid expense", {
              entryDate: [`Travel claim ${claim.id} already holds a per diem day on ${input.entryDate}`],
            }),
          );
        }
        const { line, refusal } = pricePerDiem(input, claim, metaOf().defaultCurrency);
        if (refusal) return Promise.resolve(problem(400, "Invalid expense", refusal));
        perDiem = line;
      }
      const saved: Expense = {
        id: takeId(),
        kind: input.kind as Expense["kind"],
        entryDate: input.entryDate,
        // The contract lets a per diem day arrive with no description — what
        // the day *is* names it, and the client renders `perDiem.type`.
        description: input.description ?? "",
        category: input.categoryId ? categoryStore.find((one) => one.id === input.categoryId) : undefined,
        billable: input.billable ?? false,
        // A line takes its claim's owner, project and status; only a
        // standalone expense carries a flow of its own.
        ...(claim
          ? {
              claimId: claim.id,
              status: claim.status,
              owner: claim.owner,
              ...(claim.project ? { project: claim.project } : {}),
            }
          : { status: "draft" as const, owner: { userId: me, displayName: "Ada Lovelace", active: true } }),
        attachmentCount: 0,
        attachments: [],
        revision: 1,
        createdAt: "2026-09-19T09:00:00Z",
        updatedAt: "2026-09-19T09:00:00Z",
        // A claim's line carries **no flow of its own**: it is never
        // submitted, approved or reimbursed by itself, and it may be changed
        // only while its claim may be. The server answers exactly this, and a
        // fake that handed every line `ownDraftCapabilities` would let a
        // future "submit this line" control pass every test.
        capabilities: claim
          ? capabilitiesOf({
              ...noCapabilities,
              canEdit: claim.capabilities.canEdit,
              canDelete: claim.capabilities.canEdit,
            })
          : ownDraftCapabilities,
        ...(perDiem ?? priced(input, ratesOf(), metaOf().defaultCurrency)),
      } as StoredExpense;
      entries.push(saved);
      return Promise.resolve(jsonResponse(201, renderEntry(saved)));
    }

    if (path === "/api/v1/expenses/entries" && method === "GET") {
      const query = url.searchParams;
      const userId = query.get("userId");
      const from = query.get("from");
      const to = query.get("to");
      const reimbursed = query.get("reimbursed");
      const standalone = query.get("standalone");
      const claimId = query.get("claimId");
      const projectId = query.get("projectId");
      const badPaging = pageRefusal(query);
      if (badPaging) return Promise.resolve(badPaging);
      if (server.entryList) return Promise.resolve(server.entryList.clone());
      // `toInvoice=false` is the parameter **left out** — no filter, and none
      // of the rules below — which is what an unticked box asks for. `true`
      // is read one project at a time, is only ever about approved expenses
      // and never names a per diem day: each contradiction is refused rather
      // than answered with an empty page that would not say which was wrong.
      const toInvoice = query.get("toInvoice") === "true";
      if (toInvoice) {
        const errors: string[] = [];
        if (projectId === null) {
          errors.push("'toInvoice=true' needs a 'projectId': what is ready to invoice is read one project at a time.");
        }
        const status = query.get("status");
        if (status && status !== "approved") {
          errors.push(
            `'toInvoice=true' is only ever about approved expenses, so it cannot be combined with 'status=${status}'.`,
          );
        }
        if (query.get("kind") === "per_diem") {
          errors.push("'toInvoice=true' never names a per diem day, so it cannot be combined with 'kind=per_diem'.");
        }
        if (errors.length > 0) {
          return Promise.resolve(problem(400, "Invalid query parameters", { toInvoice: errors }));
        }
        // Only now: the server validates the query and *then* asks the
        // directory, so a contradictory query from a refused caller is a 400,
        // not a 403. What a line bills is the project's money, so the filter
        // is for the same callers the summary is — everyone else gets the
        // access layer's uniform refusal, not an empty page that would read
        // as "nothing is ready".
        if (server.refuseToInvoice ?? summaryRefused()) return Promise.resolve(forbidden());
      }
      const matching = entries
        .filter((entry) => !userId || entry.owner.userId === userId)
        .filter((entry) => projectId === null || entry.project?.id === Number(projectId))
        .filter((entry) => !toInvoice || isReady(entry))
        // A claim's lines are the trip's, so "My expenses" asks for the units
        // of their own and the trip is listed beside them rather than twice.
        .filter((entry) => standalone === null || (entry.claimId === undefined) === (standalone === "true"))
        .filter((entry) => claimId === null || entry.claimId === Number(claimId))
        // The **unit's** status, `COALESCE(claim.status, entry.status)`: a
        // trip's line is filtered by its trip, exactly as every other read of
        // this module judges it.
        .filter((entry) => !query.get("status") || unitStatusOf(entry) === query.get("status"))
        .filter((entry) => !query.get("kind") || entry.kind === query.get("kind"))
        .filter((entry) => !from || entry.entryDate >= from)
        .filter((entry) => !to || entry.entryDate <= to)
        .filter((entry) => reimbursed === null || (entry.reimbursement !== undefined) === (reimbursed === "true"))
        .sort((a, b) => (a.entryDate === b.entryDate ? b.id - a.id : a.entryDate < b.entryDate ? 1 : -1));
      return maybeHold(
        `${path}${url.search}`,
        Promise.resolve(
          jsonResponse(
            200,
            page(renderEntries(matching), Number(query.get("page") ?? 1), server.pageSize ?? LIST_DEFAULT_PAGE_SIZE),
          ),
        ),
      );
    }

    return Promise.resolve(new Response(null, { status: 404 }));
  }) as ExpensesStub;
  stub.release = releaseHeld;
  return stub;
};
