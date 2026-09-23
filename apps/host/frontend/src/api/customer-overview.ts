import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * GET /customers/{id}/overview (customer 360 design D1, D2) as the host's
 * panel reads it. The server OMITS a section the caller may not see or whose
 * module is off, and every unset optional field inside one; this boundary turns
 * each omission into null, once, so the panel asks `=== null` rather than
 * guessing between undefined, missing and empty. An absent `unbilledAmounts`
 * (no financial rights) and an empty one (nothing unbilled) stay different.
 *
 * The types are written out here rather than read from a generated schema, the
 * dashboard's own rule for another module's payload: the host's api-schema.d.ts
 * is identity's, and customers-ui's belongs to that package.
 */
export interface OverviewAmount {
  currency: string;
  amount: number;
}

export interface OverviewOpenProject {
  id: number;
  code: string;
  name: string;
  status: string;
  lastWorkOn: string | null;
}

export interface CustomerOverview {
  projects: { openCount: number; totalCount: number; truncated: boolean; open: OverviewOpenProject[] } | null;
  work: {
    unbilledHoursHundredths: number;
    unbilledAmounts: OverviewAmount[] | null;
    approvedHoursHundredths: number;
    submittedHoursHundredths: number;
    draftHoursHundredths: number;
    lastWorkOn: string | null;
  } | null;
  expenses: { readyCount: number; readyAmounts: OverviewAmount[]; lastExpenseOn: string | null } | null;
  lastActivity: { timelineOn: string | null; workOn: string | null; expenseOn: string | null };
}

/** The wire shape: every optional field may simply be missing. */
interface CustomerOverviewWire {
  projects?: {
    openCount: number;
    totalCount: number;
    truncated: boolean;
    open: (Omit<OverviewOpenProject, "lastWorkOn"> & { lastWorkOn?: string })[];
  };
  work?: {
    unbilledHoursHundredths: number;
    unbilledAmounts?: OverviewAmount[];
    approvedHoursHundredths: number;
    submittedHoursHundredths: number;
    draftHoursHundredths: number;
    lastWorkOn?: string;
  };
  expenses?: { readyCount: number; readyAmounts: OverviewAmount[]; lastExpenseOn?: string };
  lastActivity?: { timelineOn?: string; workOn?: string; expenseOn?: string };
}

export const normaliseCustomerOverview = (wire: CustomerOverviewWire): CustomerOverview => ({
  projects: wire.projects
    ? {
        openCount: wire.projects.openCount,
        totalCount: wire.projects.totalCount,
        truncated: wire.projects.truncated,
        open: wire.projects.open.map((project) => ({ ...project, lastWorkOn: project.lastWorkOn ?? null })),
      }
    : null,
  work: wire.work
    ? { ...wire.work, unbilledAmounts: wire.work.unbilledAmounts ?? null, lastWorkOn: wire.work.lastWorkOn ?? null }
    : null,
  expenses: wire.expenses
    ? {
        readyCount: wire.expenses.readyCount,
        readyAmounts: wire.expenses.readyAmounts,
        lastExpenseOn: wire.expenses.lastExpenseOn ?? null,
      }
    : null,
  // Always present on the wire (D2); read defensively all the same, because a
  // missing object here would take the whole panel down rather than one tile.
  lastActivity: {
    timelineOn: wire.lastActivity?.timelineOn ?? null,
    workOn: wire.lastActivity?.workOn ?? null,
    expenseOn: wire.lastActivity?.expenseOn ?? null,
  },
});

/**
 * Keyed under `["customers", id]` so the customers package's broad writes —
 * those that invalidate `["customers"]` whole (the edit form, tags, archive and
 * restore, the contact card) — refresh the panel without this file knowing
 * about them. The narrow ones do NOT: timeline writes invalidate
 * `["customers", id, "timeline"]` and contact-association writes their own
 * keys, neither of which is a prefix of this one, so after a new timeline note
 * the Last activity tile catches up within `staleTime` or on the next mount.
 * Closing that needs the package to tell the host about its writes, which is a
 * new prop D3 rules out; it is a follow-up, not a re-key under `"timeline"`.
 */
export const customerOverviewQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "overview"] as const,
    queryFn: async ({ signal }) =>
      normaliseCustomerOverview(
        await request<CustomerOverviewWire>(`/api/v1/customers/${customerId}/overview`, { signal }),
      ),
    staleTime: 60_000,
  });
