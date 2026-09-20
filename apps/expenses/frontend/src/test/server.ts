import type { Expense, ExpenseAttachment, ExpenseInput, ExpenseUpdateInput } from "../api/entries";
import type { ExpensesMeta } from "../api/meta";
import type { ExpenseProjectOption } from "../api/projects";
import type { ExpenseRate } from "../api/rates";
import type { ExpenseStats } from "../api/stats";
import { round2 } from "../lib/money";
import { mileagePreview } from "../lib/rates";
import { jsonResponse } from "./api";
import { stubFetch } from "./fetch";
import { categories, meta as defaultMeta, rates as defaultRates, ME, ownDraftCapabilities, stats } from "./fixtures";

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

/**
 * The Expenses API as the pages read and write it, from an in-memory store.
 * It answers `/meta`, `/entries` (list, read, create, replace, delete),
 * `/submit`, `/projects`, `/rates`, `/stats` and the two receipt operations;
 * `write` and `upload` let a test put a refusal in the place of any of them.
 * Task 7 extends it with `/approvals`, `/reimbursements` and the settings
 * reads, in the same shape.
 */
export const stubExpensesApi = (server: ExpensesServer = {}) => {
  const entries = server.entries ?? [];
  const metaOf = (): ExpensesMeta =>
    server.meta instanceof Response ? defaultMeta() : (server.meta ?? defaultMeta({ categories }));
  const ratesOf = (): ExpenseRate[] =>
    server.rates instanceof Response ? defaultRates : (server.rates ?? defaultRates);
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

  const find = (id: number) => entries.find((entry) => entry.id === id);

  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    const isForm = typeof FormData !== "undefined" && init?.body instanceof FormData;
    const body = init?.body && !isForm ? JSON.parse(String(init.body)) : undefined;
    const answered = method === "GET" ? undefined : server.write?.(method, path, body);
    if (answered) return Promise.resolve(answered);

    if (path === "/api/v1/expenses/meta") return Promise.resolve(answer(server.meta, defaultMeta({ categories })));
    if (path === "/api/v1/expenses/projects") return Promise.resolve(answer(server.projects, []));
    if (path === "/api/v1/expenses/rates") return Promise.resolve(answer(server.rates, defaultRates));
    if (path === "/api/v1/expenses/stats") return Promise.resolve(answer(server.stats, stats()));

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
          category: update.categoryId ? categories.find((one) => one.id === update.categoryId) : undefined,
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
        description: input.description,
        category: input.categoryId ? categories.find((one) => one.id === input.categoryId) : undefined,
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
