import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import type { ManufacturingOrder } from "./orders";
import { ORDERS_POLL_MS } from "./orders";
import { json, MANUFACTURING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type ManufacturingSchedule = Schemas["ManufacturingSchedule"];
export type ManufacturingScheduleRow = Schemas["ManufacturingScheduleRow"];
export type ManufacturingScheduleDay = Schemas["ManufacturingScheduleDay"];
export type ManufacturingScheduleBar = Schemas["ManufacturingScheduleBar"];
export type ManufacturingScheduleOrder = Schemas["ManufacturingScheduleOrder"];
export type ManufacturingPlanAllResult = Schemas["ManufacturingPlanAllResult"];
export type ManufacturingRescheduleInput = Schemas["ManufacturingRescheduleInput"];

/** How many days the board shows: a week (the default) or two (M36). */
export type ScheduleSpan = 7 | 14;
export const scheduleSpans: readonly ScheduleSpan[] = [7, 14];

export interface ScheduleFilter {
  siteId: number;
  /** The first day; the business calendar's today when absent. */
  from?: string;
  days: ScheduleSpan;
}

/** The board's query string (`GET /schedule`): the server's defaults are not sent. */
export const scheduleQueryString = (filter: ScheduleFilter): string =>
  queryString({ siteId: filter.siteId, from: filter.from, days: filter.days === 7 ? undefined : filter.days });

/** The board of a site from a day (M36). Polled while its live topics are down. */
export const scheduleQueryOptions = (filter: ScheduleFilter, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "schedule", filter.siteId, filter.from ?? "", filter.days],
    queryFn: ({ signal }) =>
      request<ManufacturingSchedule>(`/api/v1/manufacturing/schedule${scheduleQueryString(filter)}`, { signal }),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** Places the order's unfinished work orders forward on their calendars (M35); 409 `no_capacity` past 365 days. */
export const planOrder = (orderId: number, revision: number) =>
  send<ManufacturingOrder>(`/api/v1/manufacturing/orders/${orderId}/plan`, json("POST", { revision }));

/** Clears the order's planned dates and frees its time (M35). */
export const unplanOrder = (orderId: number, revision: number) =>
  send<ManufacturingOrder>(`/api/v1/manufacturing/orders/${orderId}/unplan`, json("POST", { revision }));

/** Plans every confirmed, unplanned order of the site, urgent first, then by due date (M35). */
export const planAllOrders = (siteId: number) =>
  send<ManufacturingPlanAllResult>("/api/v1/manufacturing/schedule/plan-all", json("POST", { siteId }));

/** Re-plans one work order to start at `start` under its revision (M36). */
export const rescheduleWorkOrder = (workOrderId: number, input: ManufacturingRescheduleInput) =>
  send<Schemas["ManufacturingWorkOrderList"]>(
    `/api/v1/manufacturing/work-orders/${workOrderId}/reschedule`,
    json("POST", input),
  );

/**
 * Listens to the board's live topics (M36) — the site's
 * `manufacturing.schedule:<siteId>` and the orders' keyless list topic — and
 * reads the board again on an event of either. Returns whether both are
 * live: the board polls while one is not.
 */
export function useScheduleLive(siteId: number | undefined): boolean {
  const client = useQueryClient();
  const refetch = () => void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "schedule"] });
  const schedule = useLiveTopic(siteId === undefined ? undefined : `manufacturing.schedule:${siteId}`, refetch);
  const orders = useLiveTopic("manufacturing.orders", () => {
    refetch();
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "orders"] });
  });
  return schedule && orders;
}
