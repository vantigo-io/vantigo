import type { FinalPeriod, SubscriptionBilling, SubscriptionInterval } from "../api/types";

/** A subscription's plan as a form holds it (O42). */
export interface PlanState {
  interval: SubscriptionInterval;
  billing: SubscriptionBilling;
  startDate: string | null;
  endDate: string | null;
  noticeMonths: number | "";
  invoiceDay: number | "";
  /** How the period the end falls in is billed; null leaves a new subscription to the settings' default. */
  finalPeriod: FinalPeriod | null;
}

export const FINAL_PERIODS: readonly FinalPeriod[] = ["full", "prorated"];

export const INTERVALS: readonly SubscriptionInterval[] = ["monthly", "quarterly", "half_yearly", "yearly"];

/** A plan's fields as the API takes them; the start is required and is checked by the caller. */
export const planInput = (plan: PlanState) => ({
  interval: plan.interval,
  billing: plan.billing,
  startDate: plan.startDate ?? "",
  endDate: plan.endDate,
  noticeMonths: plan.noticeMonths === "" ? 0 : plan.noticeMonths,
  invoiceDay: plan.invoiceDay === "" ? null : plan.invoiceDay,
  ...(plan.finalPeriod ? { finalPeriod: plan.finalPeriod } : {}),
});
