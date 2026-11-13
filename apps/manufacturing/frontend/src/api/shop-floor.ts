import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveClient, useLiveTopic } from "@vantigo/frontend-shell";
import { useEffect, useRef } from "react";
import type { components } from "../api-schema";
import { ORDERS_POLL_MS } from "./orders";
import { json, MANUFACTURING_QUERY_KEY, request, send } from "./request";

type Schemas = components["schemas"];

export type ManufacturingWorkOrder = Schemas["ManufacturingWorkOrder"];
export type ManufacturingWorkOrderStatus = Schemas["ManufacturingWorkOrderStatus"];
export type ManufacturingWorkOrderView = Schemas["ManufacturingWorkOrderView"];
export type ManufacturingWorkCenterQueue = Schemas["ManufacturingWorkCenterQueue"];
export type ManufacturingWorkCenterBlock = Schemas["ManufacturingWorkCenterBlock"];
export type ManufacturingDowntimeReason = Schemas["ManufacturingDowntimeReason"];
export type ManufacturingTimeLog = Schemas["ManufacturingTimeLog"];
export type ManufacturingTimeLogKind = Schemas["ManufacturingTimeLogKind"];
export type ManufacturingTimeLogCorrection = Schemas["ManufacturingTimeLogCorrection"];
export type ManufacturingTimeLogInput = Schemas["ManufacturingTimeLogInput"];
export type ManufacturingShopFloorResult = Schemas["ManufacturingShopFloorResult"];
export type ManufacturingFinishInput = Schemas["ManufacturingFinishInput"];
export type ManufacturingScrapInput = Schemas["ManufacturingScrapInput"];
export type ManufacturingMyWork = Schemas["ManufacturingMyWork"];

/** Every state of a work order (M27), in the order it goes through them. */
export const workOrderStatuses: readonly ManufacturingWorkOrderStatus[] = [
  "pending",
  "ready",
  "in_progress",
  "paused",
  "done",
  "cancelled",
];
/** Why a work center is blocked, or what a downtime log was (M29, M30). */
export const downtimeReasons: readonly ManufacturingDowntimeReason[] = [
  "breakdown",
  "material_missing",
  "setup",
  "waiting_for_operator",
  "quality",
  "other",
];
export const timeLogKinds: readonly ManufacturingTimeLogKind[] = ["productive", "downtime"];

/** Whether a work order is done or cancelled: nothing more is logged on it (M27). */
export const isClosedWorkOrder = (workOrder: Pick<ManufacturingWorkOrder, "status">) =>
  workOrder.status === "done" || workOrder.status === "cancelled";

/** An order's work orders, by sequence; read again on the order's live event. */
export const orderWorkOrdersQueryOptions = (orderId: number, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId, "work-orders"],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingWorkOrderList"]>(`/api/v1/manufacturing/orders/${orderId}/work-orders`, {
        signal,
      }).then((answer) => answer.items),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** Every time log of an order, by start; read again on the order's live event. */
export const orderTimeLogsQueryOptions = (orderId: number, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId, "time-logs"],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingTimeLogList"]>(`/api/v1/manufacturing/orders/${orderId}/time-logs`, {
        signal,
      }).then((answer) => answer.items),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** A work center's queue and its block (M31). */
export const workCenterQueueQueryOptions = (workCenterId: number, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "work-center", workCenterId],
    queryFn: ({ signal }) =>
      request<ManufacturingWorkCenterQueue>(`/api/v1/manufacturing/work-centers/${workCenterId}/queue`, { signal }),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** The work order view (M31): the work order, its instructions, its components and its open logs. */
export const workOrderQueryOptions = (workOrderId: number, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "work-order", workOrderId],
    queryFn: ({ signal }) =>
      request<ManufacturingWorkOrderView>(`/api/v1/manufacturing/work-orders/${workOrderId}`, { signal }),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** My work (M31): the caller's open log and the work orders they touched today. */
export const myWorkQueryOptions = () =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "mine"],
    queryFn: ({ signal }) => request<ManufacturingMyWork>("/api/v1/manufacturing/shop-floor/mine", { signal }),
    refetchInterval: ORDERS_POLL_MS,
  });

/** Blocks a work center with a reason and a note (M30); answers its queue. */
export const blockWorkCenter = (workCenterId: number, input: Schemas["ManufacturingBlockInput"]) =>
  send<ManufacturingWorkCenterQueue>(`/api/v1/manufacturing/work-centers/${workCenterId}/block`, json("POST", input));

export const unblockWorkCenter = (workCenterId: number) =>
  send<ManufacturingWorkCenterQueue>(`/api/v1/manufacturing/work-centers/${workCenterId}/unblock`, {
    method: "POST",
  });

/**
 * Opens a productive log for the caller (M29); `force` starts a pending work
 * order out of sequence, `workCenterId` starts a group work order at that
 * member (manufacturing phase 2 M119).
 */
export const startWorkOrder = (workOrderId: number, force?: boolean, workCenterId?: number) =>
  send<ManufacturingShopFloorResult>(
    `/api/v1/manufacturing/work-orders/${workOrderId}/start`,
    json("POST", { ...(force ? { force: true } : {}), ...(workCenterId === undefined ? {} : { workCenterId }) }),
  );

/**
 * Register quantity (manufacturing phase 2 M116): good units passed on to the
 * work orders after this one, posting nothing to stock. One `clientRequestId`
 * per registration makes a retry the same one.
 */
export const registerQuantity = (workOrderId: number, input: Schemas["ManufacturingQuantityInput"]) =>
  send<ManufacturingShopFloorResult>(`/api/v1/manufacturing/work-orders/${workOrderId}/quantity`, json("POST", input));

export const pauseWorkOrder = (workOrderId: number) =>
  send<ManufacturingShopFloorResult>(`/api/v1/manufacturing/work-orders/${workOrderId}/pause`, { method: "POST" });

/** Finishes the work order (M29); a production in the body is posted in the same transaction. */
export const finishWorkOrder = (workOrderId: number, input: ManufacturingFinishInput = {}) =>
  send<ManufacturingShopFloorResult>(`/api/v1/manufacturing/work-orders/${workOrderId}/finish`, json("POST", input));

/** Reports scrap at the work order: a production with no good output (M31). */
export const reportScrap = (workOrderId: number, input: ManufacturingScrapInput) =>
  send<Schemas["ManufacturingProduction"]>(
    `/api/v1/manufacturing/work-orders/${workOrderId}/scrap`,
    json("POST", input),
  );

/** A supervisor's correction of a log (M29a). */
export const correctTimeLog = (timeLogId: number, input: ManufacturingTimeLogCorrection) =>
  send<ManufacturingTimeLog>(`/api/v1/manufacturing/time-logs/${timeLogId}`, json("PATCH", input));

/** A missing log added by a supervisor (M29). */
export const addTimeLog = (workOrderId: number, input: ManufacturingTimeLogInput) =>
  send<ManufacturingTimeLog>(`/api/v1/manufacturing/work-orders/${workOrderId}/time-logs`, json("POST", input));

/**
 * Listens to a work center's topic (M32) and reads its queue again on an
 * event. Returns whether the topic is live: the screen polls only while not.
 */
export function useWorkCenterLive(workCenterId: number): boolean {
  const client = useQueryClient();
  return useLiveTopic(`manufacturing.work-center:${workCenterId}`, () => {
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "work-center", workCenterId] });
  });
}

/** Listens to a work order's topic (M32) and reads the view again on an event. */
export function useWorkOrderLive(workOrderId: number): boolean {
  const client = useQueryClient();
  return useLiveTopic(`manufacturing.work-order:${workOrderId}`, () => {
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "work-order", workOrderId] });
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "mine"] });
  });
}

/**
 * Listens to `manufacturing.order:<id>` of every order named while mounted
 * and calls `onEvent` on any of their events: an order's own writes — a
 * production, a reservation, a component line — change what a shop-floor
 * screen shows of it without touching a work order. One listen per order;
 * it need not be memoised.
 */
function useOrderTopics(orderIds: readonly number[], onEvent: () => void): void {
  const client = useLiveClient();
  const latest = useRef(onEvent);
  useEffect(() => {
    latest.current = onEvent;
  });
  const key = [...new Set(orderIds)].sort((a, b) => a - b).join(",");
  useEffect(() => {
    if (!client || key === "") return;
    const stops = key.split(",").map((id) => client.listen(`manufacturing.order:${id}`, () => latest.current()));
    return () => {
      for (const stop of stops) stop();
    };
  }, [client, key]);
}

/** Reads a work center's queue again on an event of an order in it. */
export function useWorkCenterOrdersLive(workCenterId: number, orderIds: readonly number[]): void {
  const client = useQueryClient();
  useOrderTopics(orderIds, () => {
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "work-center", workCenterId] });
  });
}

/** Reads a work order's view again on an event of its order. */
export function useWorkOrderOrderLive(workOrderId: number, orderId: number | undefined): void {
  const client = useQueryClient();
  useOrderTopics(orderId === undefined ? [] : [orderId], () => {
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "shop-floor", "work-order", workOrderId] });
  });
}
