import type { ModuleKey } from "../navigation";
import { request } from "./request";

export type DashboardPreset = "7d" | "30d" | "90d" | "12m" | "custom";
export type DateRange = { from: Date; to: Date };

/** The preset validateSearch below falls back to when no search params are given. */
export const DEFAULT_DASHBOARD_PRESET: DashboardPreset = "30d";

export interface DailyPoint {
  date: string;
  value: number;
}

export interface CustomerSummary {
  from: string;
  to: string;
  totalActiveCustomers: number;
  totalActiveCustomersDelta: number;
  newCustomers: number;
  newCustomersDelta: number;
  newContacts: number;
  newContactsDelta: number;
}

export interface CommunicationsSummary {
  from: string;
  to: string;
  openConversations: number;
  openConversationsDelta: number;
  newConversations: number;
  newConversationsDelta: number;
  messages: number;
  messagesDelta: number;
  closedConversations: number;
  closedConversationsDelta: number;
}

export interface ProductsSummary {
  from: string;
  to: string;
  totalActiveProducts: number;
  totalActiveProductsDelta: number;
  newProducts: number;
  newProductsDelta: number;
  statusCounts: Record<string, number>;
  statusCountDeltas: Record<string, number>;
}

export interface EnergySummary {
  from: string;
  to: string;
  meteringPointCount: number;
  meteringPointCountDelta: number;
  activeSupplyPeriods: number;
  activeSupplyPeriodsDelta: number;
  consumptionKwh: number;
  consumptionKwhDelta: number;
  previousConsumptionKwh: number;
}

export interface ProjectsSummary {
  from: string;
  to: string;
  activeProjects: number;
  activeProjectsDelta: number;
  newProjects: number;
  newProjectsDelta: number;
  /** Ready milestones the caller has financial rights on — a state, not a delta. */
  readyMilestones: number;
}

export interface TimeSummary {
  from: string;
  to: string;
  hoursThisWeek: number;
  hoursThisWeekDelta: number;
  awaitingMyApproval: number;
  awaitingMyApprovalDelta: number;
}

export interface ExpensesSummary {
  from: string;
  to: string;
  awaitingMyApproval: number;
  awaitingMyApprovalDelta: number;
  myDrafts: number;
  /** Never summed across currencies (design §4): one line per currency. */
  myUnreimbursed: { currency: string; amount: number }[];
}

/** The invoices module's `InvoicesStatsSummaryResponse` (payments and delivery design D7); all NOK. */
export interface InvoicesSummary {
  from: string;
  to: string;
  /** Now, not in the period: issued invoices with something open, credit notes excluded. */
  outstandingAmount: number;
  outstandingCount: number;
  /** Now: those of the outstanding past their due date. */
  overdueAmount: number;
  overdueCount: number;
  issuedCount: number;
  issuedGrossTotal: number;
  issuedGrossTotalDelta: number;
  creditedCount: number;
  creditedGrossTotal: number;
  paidAmount: number;
  paidCount: number;
}

/**
 * Staff's `StaffStats` (design S20): figures on today, not over the period —
 * a register has no meaningful "new people this period" — so it is fetched
 * on its own (`GET /staff/stats`), not through the summary every other card
 * shares.
 */
export interface StaffStats {
  headcount: number;
  fteSum: number;
  startingSoon: number;
  endingSoon: number;
  peopleWithoutEmployment: number;
  /** People away today on an approved or registered absence, any kind (delivery B). */
  onLeaveToday?: number;
}

export interface AttentionItem {
  id: string;
  type: string;
  title: string;
  occurredAt: string;
  entityId: string;
  /**
   * Optional, added by the Expenses module: a number a server-built title
   * cannot localise, so the host renders its own sentence from `type` and
   * this count instead of the server's English fallback.
   */
  count?: number;
}

export const presetDays: Record<Exclude<DashboardPreset, "custom">, number> = {
  "7d": 7,
  "30d": 30,
  "90d": 90,
  "12m": 365,
};

export const dateOnly = (value: Date) => value.toISOString().slice(0, 10);

export const startOfDay = (value: Date) => {
  const result = new Date(value);
  result.setHours(0, 0, 0, 0);
  return result;
};

export const endOfDay = (value: Date) => {
  const result = new Date(value);
  result.setHours(23, 59, 59, 999);
  return result;
};

export const presetRange = (preset: Exclude<DashboardPreset, "custom">): DateRange => {
  const to = endOfDay(new Date());
  const from = startOfDay(new Date(to));
  from.setDate(from.getDate() - presetDays[preset]);
  return { from, to };
};

export const buildStatsUrl = (
  module: ModuleKey,
  endpoint: "summary" | "timeseries",
  range: DateRange,
  metric?: string,
) => {
  const params = new URLSearchParams({ from: range.from.toISOString(), to: range.to.toISOString() });
  if (metric) params.set("metric", metric);
  return `/api/v1/${module}/stats/${endpoint}?${params}`;
};

export const fetchSummary = <T>(module: ModuleKey, range: DateRange, signal: AbortSignal) =>
  request<T>(buildStatsUrl(module, "summary", range), { signal });

export const fetchTimeseries = (module: ModuleKey, metric: string, range: DateRange, signal: AbortSignal) =>
  request<DailyPoint[]>(buildStatsUrl(module, "timeseries", range, metric), { signal });

export const fetchStaffStats = (signal: AbortSignal) => request<StaffStats>("/api/v1/staff/stats", { signal });

export const fetchAttention = async (module: ModuleKey, signal: AbortSignal) => {
  try {
    return await request<AttentionItem[]>(`/api/v1/${module}/stats/attention`, { signal });
  } catch {
    // Attention is deliberately best-effort: a module can be enabled while a
    // user lacks the more specific permission required by this endpoint.
    return [];
  }
};
