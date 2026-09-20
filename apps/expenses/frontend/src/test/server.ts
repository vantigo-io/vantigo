import type { ExpenseApprovalGroup, ExpenseBillingLineOption, ExpenseCurrencyTotal } from "../api/approvals";
import type { ExpenseCategory } from "../api/categories";
import type { Expense, ExpenseAttachment, ExpenseInput, ExpenseUpdateInput } from "../api/entries";
import type { ExpensesMeta } from "../api/meta";
import type { ExpenseProjectOption } from "../api/projects";
import type { ExpenseRate } from "../api/rates";
import type { ExpenseReimbursementGroup } from "../api/reimbursements";
import type { ExpenseSettings } from "../api/settings";
import type { ExpenseStats } from "../api/stats";
import { round2 } from "../lib/money";
import { mileagePreview } from "../lib/rates";
import { jsonResponse } from "./api";
import { stubFetch } from "./fetch";
import {
  APPROVER,
  categories as defaultCategories,
  meta as defaultMeta,
  rates as defaultRates,
  settings as defaultSettings,
  ME,
  ownDraftCapabilities,
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
  entries?: Expense[];
  projects?: Read<ExpenseProjectOption[]>;
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
}

/** The fixture, or the refusal the test put in its place; a Response is cloned so a refetch reads it again. */
const answer = <T>(read: Read<T> | undefined, fallback: T): Response =>
  read instanceof Response ? read.clone() : jsonResponse(200, read ?? fallback);

const page = <T>(data: T[], pageNumber: number, pageSize: number) => {
  const totalPages = Math.max(1, Math.ceil(data.length / pageSize));
  const start = (pageNumber - 1) * pageSize;
  return {
    data: data.slice(start, start + pageSize),
    pagination: {
      page: pageNumber,
      pageSize,
      totalCount: data.length,
      totalPages,
      hasNextPage: pageNumber < totalPages,
      hasPreviousPage: pageNumber > 1,
    },
  };
};

const problem = (status: number, title: string, errors?: Record<string, string[]>) =>
  jsonResponse(status, { title, status, ...(errors ? { errors } : {}) });

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

/** The store's expenses grouped per person, the one who has waited longest first. */
const groupedByOwner = (entries: Expense[]): { user: Expense["owner"]; entries: Expense[] }[] => {
  const byUser = new Map<string, Expense[]>();
  for (const entry of entries) byUser.set(entry.owner.userId, [...(byUser.get(entry.owner.userId) ?? []), entry]);
  return [...byUser.values()]
    .map((rows) => ({
      user: rows[0].owner,
      entries: [...rows].sort((a, b) =>
        a.entryDate === b.entryDate ? a.id - b.id : a.entryDate < b.entryDate ? -1 : 1,
      ),
    }))
    .sort((a, b) => (a.entries[0].entryDate < b.entries[0].entryDate ? -1 : 1));
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
 */
export const stubExpensesApi = (server: ExpensesServer = {}) => {
  const entries = server.entries ?? [];
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

  const rateStore = server.rates instanceof Response ? [...defaultRates] : (server.rates ?? [...defaultRates]);
  const ratesOf = (): ExpenseRate[] => rateStore;
  const categoryStore = server.categories ?? [...defaultCategories];
  let settingsStore = server.settings instanceof Response ? defaultSettings() : (server.settings ?? defaultSettings());

  const find = (id: number) => entries.find((entry) => entry.id === id);

  /** Every batch refuses as one, with a message per offending id on `entryIds`. */
  const missing = (title: string, ids: number[]): Response | undefined => {
    const unknown = ids.filter((id) => !find(id));
    return unknown.length === 0
      ? undefined
      : problem(400, title, { entryIds: unknown.map((id) => `Expense ${id} was not found`) });
  };

  const moveAll = (ids: number[], move: (entry: Expense) => void): Response => {
    const moved = ids.map((id) => {
      const entry = find(id) as Expense;
      move(entry);
      entry.revision += 1;
      return entry;
    });
    return jsonResponse(200, moved);
  };

  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    const isForm = typeof FormData !== "undefined" && init?.body instanceof FormData;
    const body = init?.body && !isForm ? JSON.parse(String(init.body)) : undefined;
    const answered = method === "GET" ? undefined : server.write?.(method, path, body);
    if (answered) return Promise.resolve(answered);

    if (path === "/api/v1/expenses/meta")
      return Promise.resolve(answer(server.meta, defaultMeta({ categories: categoryStore })));
    if (path === "/api/v1/expenses/projects") return Promise.resolve(answer(server.projects, []));
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
      if (server.approvals instanceof Response) return Promise.resolve(server.approvals.clone());
      const groups: ExpenseApprovalGroup[] =
        server.approvals ??
        groupedByOwner(entries.filter((entry) => entry.status === "submitted")).map((group) => ({
          user: group.user,
          entries: group.entries,
          totals: totalsOf(group.entries),
          receiptsMissing: group.entries.filter((one) => one.kind === "outlay" && one.attachmentCount === 0).length,
          overriddenRates: group.entries.filter((one) => one.rateOverride !== undefined).length,
        }));
      return Promise.resolve(
        jsonResponse(200, page(groups, Number(url.searchParams.get("page") ?? 1), server.pageSize ?? 25)),
      );
    }

    if (path === "/api/v1/expenses/approve" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const refusal = missing("Invalid approval", ids);
      if (refusal) return Promise.resolve(refusal);
      return Promise.resolve(
        moveAll(ids, (entry) => {
          entry.status = "approved";
          entry.decision = { status: "approved", at: "2026-09-20T09:00:00Z", by: APPROVER };
          entry.capabilities = { ...entry.capabilities, canApprove: false, canUnapprove: true };
        }),
      );
    }
    if (path === "/api/v1/expenses/reject" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const refusal = missing("Invalid approval", ids);
      if (refusal) return Promise.resolve(refusal);
      return Promise.resolve(
        moveAll(ids, (entry) => {
          entry.status = "rejected";
          entry.decision = { status: "rejected", at: "2026-09-20T09:00:00Z", by: APPROVER, reason: body.reason };
          entry.capabilities = { ...entry.capabilities, canApprove: false, canEdit: true, canSubmit: true };
        }),
      );
    }
    if (path === "/api/v1/expenses/unapprove" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const refusal = missing("Invalid approval", ids);
      if (refusal) return Promise.resolve(refusal);
      return Promise.resolve(
        moveAll(ids, (entry) => {
          entry.status = "draft";
          entry.decision = undefined;
          entry.submittedAt = undefined;
          entry.rateOverride = undefined;
          entry.capabilities = { ...entry.capabilities, canUnapprove: false, canEdit: true, canSubmit: true };
        }),
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
      const amount = round2(
        (entry.distanceKm ?? 0) * body.rate +
          (entry.distanceKm ?? 0) * (entry.passengerRate ?? 0) * (entry.passengers ?? 0),
      );
      entry.grossAmount = amount;
      entry.netAmount = amount;
      entry.owedToEmployee = amount;
      entry.revision += 1;
      return Promise.resolve(jsonResponse(200, entry));
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
      return Promise.resolve(jsonResponse(200, entry));
    }

    if (path === "/api/v1/expenses/reimbursements" && method === "GET") {
      if (server.reimbursements instanceof Response) return Promise.resolve(server.reimbursements.clone());
      const state = url.searchParams.get("state") ?? "waiting";
      const owed = entries.filter((entry) =>
        state === "reimbursed"
          ? entry.reimbursement !== undefined
          : entry.status === "approved" && entry.owedToEmployee > 0 && entry.reimbursement === undefined,
      );
      const groups: ExpenseReimbursementGroup[] =
        server.reimbursements ??
        groupedByOwner(owed).map((group) => ({
          user: group.user,
          entries: group.entries,
          totals: totalsOf(group.entries),
        }));
      return Promise.resolve(
        jsonResponse(200, page(groups, Number(url.searchParams.get("page") ?? 1), server.pageSize ?? 25)),
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
      const refusal = missing("Invalid reimbursement", ids);
      if (refusal) return Promise.resolve(refusal);
      return Promise.resolve(
        moveAll(ids, (entry) => {
          entry.reimbursement = {
            at: "2026-09-20T10:00:00Z",
            by: APPROVER,
            date: body.date,
            ...(body.reference ? { reference: body.reference } : {}),
          };
          entry.capabilities = { ...entry.capabilities, canMarkReimbursed: false, canUndoReimbursed: true };
        }),
      );
    }
    if (path === "/api/v1/expenses/reimbursed/undo" && method === "POST") {
      const ids: number[] = body?.entryIds ?? [];
      const refusal = missing("Invalid reimbursement", ids);
      if (refusal) return Promise.resolve(refusal);
      return Promise.resolve(
        moveAll(ids, (entry) => {
          entry.reimbursement = undefined;
          entry.capabilities = { ...entry.capabilities, canMarkReimbursed: true, canUndoReimbursed: false };
        }),
      );
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
      const missing = ids.filter((id) => !find(id));
      if (missing.length > 0) {
        return Promise.resolve(
          problem(400, "Invalid submission", { entryIds: missing.map((id) => `Expense ${id} was not found`) }),
        );
      }
      const moved = ids.map((id) => {
        const entry = find(id) as Expense;
        entry.status = "submitted";
        entry.submittedAt = "2026-09-19T10:00:00Z";
        entry.decision = undefined;
        entry.revision += 1;
        entry.capabilities = { ...entry.capabilities, canEdit: false, canDelete: false, canSubmit: false };
        return entry;
      });
      return Promise.resolve(jsonResponse(200, moved));
    }

    const one = /^\/api\/v1\/expenses\/entries\/(\d+)$/.exec(path);
    if (one) {
      const id = Number(one[1]);
      const entry = find(id);
      if (!entry) return Promise.resolve(new Response(null, { status: 404 }));
      if (method === "GET") return Promise.resolve(jsonResponse(200, entry));
      if (method === "DELETE") {
        entries.splice(entries.indexOf(entry), 1);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (method === "PUT") {
        const update = body as ExpenseUpdateInput;
        if (update.revision !== entry.revision) {
          return Promise.resolve(jsonResponse(409, { title: "The expense has moved on", status: 409 }));
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
        return Promise.resolve(jsonResponse(200, entry));
      }
    }

    if (path === "/api/v1/expenses/entries" && method === "POST") {
      const input = body as ExpenseInput;
      const saved: Expense = {
        id: takeId(),
        kind: input.kind as Expense["kind"],
        entryDate: input.entryDate,
        // The contract lets a per diem day arrive with no description — the
        // real server names it after the kind of day it is. Nothing here
        // records one yet, so an absent description is simply empty.
        description: input.description ?? "",
        category: input.categoryId ? categoryStore.find((one) => one.id === input.categoryId) : undefined,
        billable: input.billable ?? false,
        status: "draft",
        attachmentCount: 0,
        attachments: [],
        owner: { userId: me, displayName: "Ada Lovelace", active: true },
        revision: 1,
        createdAt: "2026-09-19T09:00:00Z",
        updatedAt: "2026-09-19T09:00:00Z",
        capabilities: ownDraftCapabilities,
        ...priced(input, ratesOf(), metaOf().defaultCurrency),
      };
      entries.push(saved);
      return Promise.resolve(jsonResponse(201, saved));
    }

    if (path === "/api/v1/expenses/entries" && method === "GET") {
      const query = url.searchParams;
      const userId = query.get("userId");
      const from = query.get("from");
      const to = query.get("to");
      const reimbursed = query.get("reimbursed");
      const matching = entries
        .filter((entry) => !userId || entry.owner.userId === userId)
        .filter((entry) => !query.get("status") || entry.status === query.get("status"))
        .filter((entry) => !query.get("kind") || entry.kind === query.get("kind"))
        .filter((entry) => !from || entry.entryDate >= from)
        .filter((entry) => !to || entry.entryDate <= to)
        .filter((entry) => reimbursed === null || (entry.reimbursement !== undefined) === (reimbursed === "true"))
        .sort((a, b) => (a.entryDate === b.entryDate ? b.id - a.id : a.entryDate < b.entryDate ? 1 : -1));
      return Promise.resolve(jsonResponse(200, page(matching, Number(query.get("page") ?? 1), server.pageSize ?? 25)));
    }

    return Promise.resolve(new Response(null, { status: 404 }));
  });
};
