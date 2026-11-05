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
/** Deletes a draft as it was read: `expectedUpdatedAt` is its `updatedAt` (409 `version_conflict` otherwise). */
export const deleteOrder = (id: number, expectedUpdatedAt: string) =>
  send<void>(`${base}/${id}?expectedUpdatedAt=${encodeURIComponent(expectedUpdatedAt)}`, { method: "DELETE" });

/**
 * What every write sends of the order as the page read it (O26): its version
 * and its `updatedAt`, the concurrency token a hold or a lock moves too.
 */
export interface OrderRead {
  version: number;
  updatedAt: string;
}
const token = (read: OrderRead) => ({ version: read.version, expectedUpdatedAt: read.updatedAt });

/** The state changes that take only the order as the page read it (O18). */
export type OrderTransition = "confirm" | "release" | "lock" | "unlock" | "cancel" | "cancel-remaining";
/** `extra` carries a confirm's warning override (O33) when one was given. */
export const transitionOrder = (id: number, transition: OrderTransition, read: OrderRead, extra: object = {}) =>
  send<Order>(`${base}/${id}/${transition}`, json("POST", { ...token(read), ...extra }));
/** Releases every draft invoice allocation of the order (O36). */
export const releaseDraftAllocations = (id: number, read: OrderRead) =>
  send<Order>(`${base}/${id}/release-draft-allocations`, json("POST", token(read)));
export const holdOrder = (id: number, read: OrderRead, reason: string) =>
  send<Order>(`${base}/${id}/hold`, json("POST", { ...token(read), ...(reason ? { reason } : {}) }));
export const markDelivered = (id: number, read: OrderRead, lines: DeliveredLine[]) =>
  send<Order>(`${base}/${id}/mark-delivered`, json("POST", { ...token(read), lines }));
export const undoDelivered = (id: number, read: OrderRead, lines: DeliveredLine[]) =>
  send<Order>(`${base}/${id}/undo-delivered`, json("POST", { ...token(read), lines }));
export const sendOrder = (id: number, input: SendInput) => send<Order>(`${base}/${id}/send`, json("POST", input));
/** Reserve now (O46): what of each goods line's open quantity is not reserved yet, at the order's site. */
export const reserveOrder = (id: number, read: OrderRead) =>
  send<Order>(`${base}/${id}/reserve`, json("POST", token(read)));

/** Where an order confirmation's PDF is: a version's stored one, or the current one printed now (O24). */
export const orderPdfUrl = (id: number, version?: number) =>
  `${base}/${id}/pdf${version === undefined ? "" : `?version=${version}`}`;
export const orderProformaUrl = (id: number) => `${base}/${id}/proforma`;
