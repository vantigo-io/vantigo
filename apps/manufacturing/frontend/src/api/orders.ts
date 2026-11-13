import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { json, MANUFACTURING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type ManufacturingOrder = Schemas["ManufacturingOrder"];
export type ManufacturingOrderSummary = Schemas["ManufacturingOrderSummary"];
export type ManufacturingOrderPage = Schemas["ManufacturingOrderPage"];
export type ManufacturingOrderComponent = Schemas["ManufacturingOrderComponent"];
export type ManufacturingOrderInput = Schemas["ManufacturingOrderInput"];
export type ManufacturingOrderUpdate = Schemas["ManufacturingOrderUpdate"];
export type ManufacturingOrderEvent = Schemas["ManufacturingOrderEvent"];
export type ManufacturingOrderEventKind = Schemas["ManufacturingOrderEventKind"];
export type ManufacturingOrderLink = Schemas["ManufacturingOrderLink"];
export type ManufacturingOrderStatus = Schemas["ManufacturingOrderStatus"];
export type ManufacturingOrderSort = Schemas["ManufacturingOrderSort"];
export type ManufacturingOrigin = Schemas["ManufacturingOrigin"];
export type ManufacturingPriority = Schemas["ManufacturingPriority"];
export type ManufacturingAvailability = Schemas["ManufacturingAvailability"];
export type ManufacturingReservationStatus = Schemas["ManufacturingReservationStatus"];
export type ManufacturingShortLine = Schemas["ManufacturingShortLine"];
export type ManufacturingOrderWithShortLines = Schemas["ManufacturingOrderWithShortLines"];
export type ManufacturingCloseResult = Schemas["ManufacturingCloseResult"];
export type ManufacturingComponentInput = Schemas["ManufacturingComponentInput"];
export type ManufacturingComponentUpdate = Schemas["ManufacturingComponentUpdate"];
export type ManufacturingProductionDraft = Schemas["ManufacturingProductionDraft"];
export type ManufacturingProductionInput = Schemas["ManufacturingProductionInput"];
export type ManufacturingProduction = Schemas["ManufacturingProduction"];
export type ManufacturingSalesOrderLine = Schemas["ManufacturingSalesOrderLine"];
export type ManufacturingChildrenCreated = Schemas["ManufacturingChildrenCreated"];
export type ManufacturingProductionDraftLine = Schemas["ManufacturingProductionDraftLine"];
export type ManufacturingLotChoice = Schemas["ManufacturingLotChoice"];
export type ManufacturingProductionLotInput = Schemas["ManufacturingProductionLotInput"];
export type ManufacturingOrderLots = Schemas["ManufacturingOrderLots"];
export type ManufacturingOrderOutput = Schemas["ManufacturingOrderOutput"];
export type ManufacturingProductionOutputInput = Schemas["ManufacturingProductionOutputInput"];
export type ManufacturingProductionDraftByproduct = Schemas["ManufacturingProductionDraftByproduct"];
export type ManufacturingByproductCost = Schemas["ManufacturingByproductCost"];
export type ManufacturingOrderSplitResult = Schemas["ManufacturingOrderSplitResult"];
export type ManufacturingOrderMergeResult = Schemas["ManufacturingOrderMergeResult"];
export type ManufacturingUnbuild = Schemas["ManufacturingUnbuild"];
export type ManufacturingUnbuildInput = Schemas["ManufacturingUnbuildInput"];
export type ManufacturingUnbuildList = Schemas["ManufacturingUnbuildList"];

/** Every state of an order (M19), in the order it goes through them. */
export const orderStatuses: readonly ManufacturingOrderStatus[] = [
  "draft",
  "confirmed",
  "in_progress",
  "done",
  "cancelled",
];
/** The statuses the list shows when none is asked for (M24). */
export const openOrderStatuses: readonly ManufacturingOrderStatus[] = ["draft", "confirmed", "in_progress"];
export const orderSorts: readonly ManufacturingOrderSort[] = ["dueDate", "number", "priority"];
export const orderOrigins: readonly ManufacturingOrigin[] = ["manual", "sales_order", "parent", "reorder"];
export const priorities: readonly ManufacturingPriority[] = ["normal", "urgent"];
export const availabilities: readonly ManufacturingAvailability[] = ["ready", "partly_available", "waiting"];
export const reservationStatuses: readonly ManufacturingReservationStatus[] = [
  "reserved",
  "partly_reserved",
  "not_reserved",
];
export const orderEventKinds: readonly ManufacturingOrderEventKind[] = [
  "created",
  "updated",
  "confirmed",
  "reserved",
  "production_posted",
  "line_added",
  "line_changed",
  "closed",
  "cancelled",
  "backorder_created",
  "time_log_corrected",
  "time_log_added",
  "planned",
  "unplanned",
  "rescheduled",
  "children_created",
  "origin_cancelled",
  "check_recorded",
  "deviation_opened",
  "deviation_closed",
  "split",
  "split_from",
  "merged",
  "merged_into",
  "unbuilt",
];

/** A page of the order list. */
export const ORDER_PAGE_SIZE = 50;
/** How often a list or an order is read again while its live topic is down. */
export const ORDERS_POLL_MS = 30_000;

/** Whether an order is confirmed or in progress: open for production, reserving and its lines (M19). */
export const isOpenOrder = (order: Pick<ManufacturingOrder, "status">) =>
  order.status === "confirmed" || order.status === "in_progress";

export interface OrderFilter {
  /** Any of the statuses; the open ones when absent or empty. */
  status?: readonly ManufacturingOrderStatus[];
  siteId?: number;
  workCenterId?: number;
  variantId?: number;
  dueFrom?: string;
  dueTo?: string;
  late?: boolean;
  origin?: ManufacturingOrigin;
  responsibleUserId?: string;
  reservation?: ManufacturingReservationStatus;
  q?: string;
  sort?: ManufacturingOrderSort;
  page?: number;
  pageSize?: number;
}

/**
 * The list's query string (`GET /orders`): each status repeated, as the
 * contract's exploded form array; only the filters that are set. The default
 * statuses and sort are the server's, so they are not sent.
 */
export const ordersQueryString = (filter: OrderFilter): string => {
  const search = new URLSearchParams();
  for (const status of filter.status ?? []) search.append("status", status);
  const scalars = queryString({
    siteId: filter.siteId,
    workCenterId: filter.workCenterId,
    variantId: filter.variantId,
    dueFrom: filter.dueFrom,
    dueTo: filter.dueTo,
    late: filter.late || undefined,
    origin: filter.origin,
    responsibleUserId: filter.responsibleUserId,
    reservation: filter.reservation,
    q: filter.q,
    sort: filter.sort,
    page: filter.page && filter.page > 1 ? filter.page : undefined,
    pageSize: filter.pageSize,
  });
  for (const [key, value] of new URLSearchParams(scalars.slice(1))) search.append(key, value);
  const text = search.toString();
  return text ? `?${text}` : "";
};

/** A page of orders and how many match; the open ones unless other statuses are asked for. Polled while not live. */
export const ordersQueryOptions = (filter: OrderFilter = {}, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "orders", ordersQueryString(filter)],
    queryFn: ({ signal }) =>
      request<ManufacturingOrderPage>(`/api/v1/manufacturing/orders${ordersQueryString(filter)}`, { signal }),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

export const orderQueryOptions = (orderId: number, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId],
    queryFn: ({ signal }) => request<ManufacturingOrder>(`/api/v1/manufacturing/orders/${orderId}`, { signal }),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** The order's timeline, oldest first (M26). */
export const orderEventsQueryOptions = (orderId: number, live = false) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId, "events"],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingOrderEventList"]>(`/api/v1/manufacturing/orders/${orderId}/events`, {
        signal,
      }).then((answer) => answer.items),
    refetchInterval: live ? false : ORDERS_POLL_MS,
  });

/** The production dialog's prefill (M22a) at a quantity and output scrap; the server's defaults when absent. */
export const productionDraftQueryOptions = (orderId: number, quantity?: string, outputScrap?: string) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId, "production-draft", quantity ?? "", outputScrap ?? ""],
    queryFn: ({ signal }) =>
      request<ManufacturingProductionDraft>(
        `/api/v1/manufacturing/orders/${orderId}/production-draft${queryString({ quantity, outputScrap })}`,
        { signal },
      ),
  });

/** The lots the order's productions made and consumed (manufacturing phase 2 M83), for the Lots tab. */
export const orderLotsQueryOptions = (orderId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId, "lots"],
    queryFn: ({ signal }) =>
      request<ManufacturingOrderLots>(`/api/v1/manufacturing/orders/${orderId}/lots`, { signal }),
  });

export const productionQueryOptions = (productionId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "production", productionId],
    queryFn: ({ signal }) =>
      request<ManufacturingProduction>(`/api/v1/manufacturing/productions/${productionId}`, { signal }),
  });

/**
 * A sales order line, read through Orders, that a new order is made for
 * (M45): its variant, open quantity, delivery date and site, the variant's
 * default BoM and the orders already made for it.
 */
export const salesOrderLineQueryOptions = (lineId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "sales-order-line", lineId],
    queryFn: ({ signal }) =>
      request<ManufacturingSalesOrderLine>(`/api/v1/manufacturing/sales-order-lines/${lineId}`, { signal }),
  });

/** Makes one confirmed sub-assembly order per chosen component line (M47). */
export const createChildOrders = (orderId: number, revision: number, componentIds: readonly number[]) =>
  send<ManufacturingChildrenCreated>(
    `/api/v1/manufacturing/orders/${orderId}/children`,
    json("POST", { revision, componentIds }),
  );

/** A new draft order (M17): 201 with the order. */
export const createOrder = (input: ManufacturingOrderInput) =>
  send<ManufacturingOrder>("/api/v1/manufacturing/orders", json("POST", input));

/** Replaces the order's fields under its revision (M19). */
export const updateOrder = (orderId: number, input: ManufacturingOrderUpdate) =>
  send<ManufacturingOrder>(`/api/v1/manufacturing/orders/${orderId}`, json("PUT", input));

/** Confirms a draft: the snapshot and the reservation; never refused for stock (M20a). */
export const confirmOrder = (orderId: number, revision: number) =>
  send<ManufacturingOrderWithShortLines>(`/api/v1/manufacturing/orders/${orderId}/confirm`, json("POST", { revision }));

/** Reserve now: retries every line left short (M20a). */
export const reserveOrder = (orderId: number, revision: number) =>
  send<ManufacturingOrderWithShortLines>(`/api/v1/manufacturing/orders/${orderId}/reserve`, json("POST", { revision }));

export const addOrderComponent = (orderId: number, input: ManufacturingComponentInput) =>
  send<ManufacturingOrderWithShortLines>(`/api/v1/manufacturing/orders/${orderId}/components`, json("POST", input));

export const updateOrderComponent = (orderId: number, componentId: number, input: ManufacturingComponentUpdate) =>
  send<ManufacturingOrderWithShortLines>(
    `/api/v1/manufacturing/orders/${orderId}/components/${componentId}`,
    json("PUT", input),
  );

/** Registers a production (M22): 201 first, 200 for a replay of the same clientRequestId. */
export const postProduction = (orderId: number, input: ManufacturingProductionInput) =>
  send<ManufacturingProduction>(`/api/v1/manufacturing/orders/${orderId}/productions`, json("POST", input));

/** Closes the order (M23); `backorder` answers "Create backorder?" when less than the quantity was produced. */
export const closeOrder = (orderId: number, revision: number, backorder?: boolean) =>
  send<ManufacturingCloseResult>(
    `/api/v1/manufacturing/orders/${orderId}/close`,
    json("POST", backorder === undefined ? { revision } : { revision, backorder }),
  );

export const cancelOrder = (orderId: number, revision: number) =>
  send<ManufacturingOrder>(`/api/v1/manufacturing/orders/${orderId}/cancel`, json("POST", { revision }));

/** Splits a draft or confirmed order into quantities summing to its own, the first kept (manufacturing phase 2 M124). */
export const splitOrder = (orderId: number, revision: number, quantities: readonly string[]) =>
  send<ManufacturingOrderSplitResult>(
    `/api/v1/manufacturing/orders/${orderId}/split`,
    json("POST", { revision, quantities }),
  );

/** Merges orders into the one with the lowest number (M125). */
export const mergeOrders = (orders: readonly { id: number; revision: number }[]) =>
  send<ManufacturingOrderMergeResult>("/api/v1/manufacturing/orders/merge", json("POST", { orders }));

/** A page of unbuilds, newest first; of one order with orderId (M123). */
export const unbuildsQueryOptions = (filter: { orderId?: number; page?: number } = {}) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "unbuilds", filter.orderId ?? 0, filter.page ?? 1],
    queryFn: ({ signal }) =>
      request<ManufacturingUnbuildList>(
        `/api/v1/manufacturing/unbuilds${queryString({ orderId: filter.orderId, page: filter.page && filter.page > 1 ? filter.page : undefined })}`,
        { signal },
      ),
  });

/** Takes finished goods apart (M123): 201, or 200 for a replay of the same clientRequestId. */
export const createUnbuild = (input: ManufacturingUnbuildInput) =>
  send<ManufacturingUnbuild>("/api/v1/manufacturing/unbuilds", json("POST", input));

/** One unbuild with what it restored (M123). */
export const unbuildQueryOptions = (unbuildId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "unbuild", unbuildId],
    queryFn: ({ signal }) => request<ManufacturingUnbuild>(`/api/v1/manufacturing/unbuilds/${unbuildId}`, { signal }),
  });

/** Releases the quarantine an unbuild of a held lot put on what it restored (M123); its own holds only. */
export const releaseUnbuild = (unbuildId: number) =>
  send<ManufacturingUnbuild>(`/api/v1/manufacturing/unbuilds/${unbuildId}/release`, { method: "POST" });

/**
 * Listens to the orders' live topics (M25) and refetches on an event: the
 * list's keyless `manufacturing.orders`, or one order's
 * `manufacturing.order:<id>` with its timeline. The list's event also reads
 * the overview's counts again, which the orders make. Returns whether the
 * topic is live: the screens poll only while it is not.
 */
export function useOrdersLive(orderId?: number): boolean {
  const client = useQueryClient();
  return useLiveTopic(orderId === undefined ? "manufacturing.orders" : `manufacturing.order:${orderId}`, () => {
    if (orderId !== undefined) {
      void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "order", orderId] });
      return;
    }
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "orders"] });
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "stats"] });
  });
}
