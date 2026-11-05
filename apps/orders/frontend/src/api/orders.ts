import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { type ListFilters, listQuery } from "./list-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type {
  Change,
  DeliveredLine,
  Mailing,
  Order,
  OrderInput,
  OrderPage,
  OrderStatus,
  OrderUpdate,
  SendInput,
  StoredDocument,
} from "./types";

export interface OrderListFilters extends ListFilters {
  status?: OrderStatus[];
}

const base = "/api/v1/orders/orders";

/** One page of orders, newest first (O18, O31a); the shown page stays while the next is fetched. */
export const orderListQueryOptions = (filters: OrderListFilters) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "orders", "list", filters],
    queryFn: ({ signal }) => request<OrderPage>(`${base}${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const orderQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "orders", "detail", id],
    queryFn: ({ signal }) => request<Order>(`${base}/${id}`, { signal }),
  });

export const orderChangesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "orders", "changes", id],
    queryFn: ({ signal }) => request<{ items: Change[] }>(`${base}/${id}/changes`, { signal }),
  });

export const orderDocumentsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "orders", "documents", id],
    queryFn: ({ signal }) => request<{ items: StoredDocument[] }>(`${base}/${id}/documents`, { signal }),
  });

export const orderMailingsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "orders", "mailings", id],
    queryFn: ({ signal }) => request<{ items: Mailing[] }>(`${base}/${id}/mailings`, { signal }),
  });

export const createOrder = (input: OrderInput) => send<Order>(base, json("POST", input));
export const updateOrder = (id: number, input: OrderUpdate) => send<Order>(`${base}/${id}`, json("PUT", input));
export const deleteOrder = (id: number) => send<void>(`${base}/${id}`, { method: "DELETE" });

/** The state changes that take only the version the page read (O18). */
export type OrderTransition = "confirm" | "release" | "lock" | "unlock" | "cancel" | "cancel-remaining";
export const transitionOrder = (id: number, transition: OrderTransition, version: number) =>
  send<Order>(`${base}/${id}/${transition}`, json("POST", { version }));
export const holdOrder = (id: number, version: number, reason: string) =>
  send<Order>(`${base}/${id}/hold`, json("POST", { version, ...(reason ? { reason } : {}) }));
export const markDelivered = (id: number, version: number, lines: DeliveredLine[]) =>
  send<Order>(`${base}/${id}/mark-delivered`, json("POST", { version, lines }));
export const undoDelivered = (id: number, version: number, lines: DeliveredLine[]) =>
  send<Order>(`${base}/${id}/undo-delivered`, json("POST", { version, lines }));
export const sendOrder = (id: number, input: SendInput) => send<Order>(`${base}/${id}/send`, json("POST", input));

/** Where an order confirmation's PDF is: a version's stored one, or the current one printed now (O24). */
export const orderPdfUrl = (id: number, version?: number) =>
  `${base}/${id}/pdf${version === undefined ? "" : `?version=${version}`}`;
export const orderProformaUrl = (id: number) => `${base}/${id}/proforma`;
