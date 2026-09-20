import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import type { Expense, FlowResult, FlowUnits, PaginatedResponse } from "./entries";
import { unitsBody } from "./entries";
import { ApiValidationError, EXPENSES_QUERY_KEY, handleUnauthorized, json, readJson, request } from "./request";

type Schemas = components["schemas"];

/** One person's expenses that are owed back to them, with the figures a payroll run is made from. */
export type ExpenseReimbursementGroup = Omit<Schemas["ExpensesReimbursementGroup"], "entries"> & {
  entries: Expense[];
};

/** What the payroll list may be narrowed by. `waiting` is the server's own default. */
export interface ReimbursementFilters {
  state?: "waiting" | "reimbursed";
  userId?: string;
  from?: string;
  to?: string;
  page?: number;
}

const filterQuery = (filters: ReimbursementFilters): URLSearchParams => {
  const query = new URLSearchParams();
  if (filters.state) query.set("state", filters.state);
  if (filters.userId) query.set("userId", filters.userId);
  if (filters.from) query.set("from", filters.from);
  if (filters.to) query.set("to", filters.to);
  if (filters.page !== undefined) query.set("page", String(filters.page));
  return query;
};

/**
 * What a payroll run is made from — approved expenses that owe their owner
 * something and have not been paid — grouped per person and paged by person,
 * so a page always holds whole people. `state=reimbursed` answers what has
 * already been paid instead, the latest payout first, so an undo is reachable.
 */
export const expenseReimbursementsQueryOptions = (filters: ReimbursementFilters) =>
  queryOptions({
    queryKey: [EXPENSES_QUERY_KEY, "reimbursements", filters],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<ExpenseReimbursementGroup>>(
        `/api/v1/expenses/reimbursements?${filterQuery(filters).toString()}`,
        { signal },
      ),
    placeholderData: keepPreviousData,
  });

/** What one payroll run pays for: the units, the day it was made and its reference. */
export type ReimbursedUnits = FlowUnits & { date: string; reference?: string };

/**
 * Marks approved units as paid back — one payroll run, with the day it was
 * made and a reference whoever made it can find it by. Standalone expenses and
 * whole travel claims go in one request; a trip is paid as one unit, for the
 * sum of what its lines owe. All or nothing, and an empty list is left out.
 */
export const markUnitsReimbursed = ({ entryIds, claimIds, ...run }: ReimbursedUnits): Promise<FlowResult> =>
  request<FlowResult>("/api/v1/expenses/reimbursed", json("POST", { ...unitsBody({ entryIds, claimIds }), ...run }));

/** Takes the reimbursement stamp back off units that were paid by mistake. */
export const undoUnitsReimbursed = (units: FlowUnits): Promise<FlowResult> =>
  request<FlowResult>("/api/v1/expenses/reimbursed/undo", json("POST", unitsBody(units)));

export interface CsvDownload {
  blob: Blob;
  fileName: string;
}

const FALLBACK_CSV_NAME = "expenses-reimbursements.csv";

/** The name the server attached the file under, or a sensible one when it said nothing. */
const fileNameFrom = (disposition: string | null): string => {
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition ?? "");
  if (encoded) return decodeURIComponent(encoded[1]);
  const plain = /filename="?([^";]+)"?/i.exec(disposition ?? "");
  return plain ? plain[1] : FALLBACK_CSV_NAME;
};

/**
 * The payroll file, fetched rather than navigated to.
 *
 * A plain `<a download>` would carry the cookie too, but it drops the person
 * on a JSON page when the export is refused — and this endpoint refuses in
 * three different shapes: field errors on `entryIds` (an id the export cannot
 * hold, or a present-but-empty list), field errors on `state`, and a row-cap
 * refusal that carries **no `errors` object at all**, only a title and a
 * detail asking for a narrower filter. Reading the body ourselves is what lets
 * all three be shown.
 *
 * A selection replaces the filters when it is given, and one that is
 * **present but names nothing at all** is refused rather than read as
 * "everything" — so "export everything" passes no selection, and a selection
 * of trips alone sends `claimIds` and no `entryIds` parameter at all. One of a
 * claim's lines named on `entryIds` is refused and pointed at its claim, even
 * when `claimIds` names that claim too: a payroll file holds a trip whole or
 * not at all.
 */
export const downloadReimbursementsCsv = async (
  filters: ReimbursementFilters,
  selection?: FlowUnits,
): Promise<CsvDownload> => {
  // A selection that names nothing at all is **no selection**, not an empty
  // one: `unitsBody` drops both empty lists, and a URL with neither would
  // reach the server as "export everything, unfiltered" — the opposite of what
  // a button with nothing ticked means. The filters go instead.
  const named = selection === undefined ? undefined : unitsBody(selection);
  const picked = named?.entryIds || named?.claimIds ? named : undefined;
  const query = picked === undefined ? filterQuery(filters) : new URLSearchParams();
  query.delete("page");
  for (const id of picked?.entryIds ?? []) query.append("entryIds", String(id));
  for (const id of picked?.claimIds ?? []) query.append("claimIds", String(id));
  const response = await fetch(appUrl(`/api/v1/expenses/reimbursements/export.csv?${query.toString()}`), {
    credentials: "include",
  });
  if (response.ok) {
    return { blob: await response.blob(), fileName: fileNameFrom(response.headers.get("Content-Disposition")) };
  }
  // The shared client is what signs somebody out; this one request does not go
  // through it, so an expired session has to be handed over by hand rather
  // than shown as a raw problem sentence.
  if (response.status === 401) await handleUnauthorized();
  const problem = await readJson<{ title?: string; detail?: string; errors?: Record<string, string[]> }>(
    response,
  ).catch(() => null);
  if (problem?.errors) {
    throw new ApiValidationError(problem.title ?? "Invalid export", problem.errors, response.status);
  }
  throw Object.assign(new Error(problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`), {
    status: response.status,
  });
};

/**
 * Hands the file to the browser, then lets go of the object URL it needed to.
 *
 * The revoke is deferred on purpose. A download is dispatched asynchronously,
 * so revoking in the same turn as the click is a race that Chromium happens to
 * win and Firefox and Safari lose — the download finds a URL that is already
 * gone, nothing is saved, and nothing says so.
 */
export const saveCsv = ({ blob, fileName }: CsvDownload): void => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  document.body.append(link);
  link.click();
  setTimeout(() => {
    link.remove();
    URL.revokeObjectURL(url);
  }, 0);
};
