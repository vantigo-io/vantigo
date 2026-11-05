import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { json, ORDERS_QUERY_KEY, request, send } from "./request";
import type { Delivery, DeliveryLineInput, DeliveryPage, DeliveryStatusOfDelivery, Site } from "./types";

const base = "/api/v1/orders/deliveries";

export interface DeliveryListFilters {
  status?: readonly DeliveryStatusOfDelivery[];
  orderId?: number;
  q?: string;
  page?: number;
  pageSize?: number;
}

const deliveryQuery = (filters: DeliveryListFilters): string => {
  const search = new URLSearchParams();
  for (const status of filters.status ?? []) search.append("status", status);
  if (filters.orderId !== undefined) search.set("orderId", String(filters.orderId));
  if (filters.q?.trim()) search.set("q", filters.q.trim());
  if (filters.page !== undefined && filters.page > 1) search.set("page", String(filters.page));
  if (filters.pageSize !== undefined) search.set("pageSize", String(filters.pageSize));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/** One page of deliveries, newest first (O47); the shown page stays while the next is fetched. */
export const deliveryListQueryOptions = (filters: DeliveryListFilters) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "deliveries", "list", filters],
    queryFn: ({ signal }) => request<DeliveryPage>(`${base}${deliveryQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const deliveryQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "deliveries", "detail", id],
    queryFn: ({ signal }) => request<Delivery>(`${base}/${id}`, { signal }),
  });

/** Inventory's sites, for the order's site and the default site (O46); empty when stock is not kept. */
export const sitesQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "sites"],
    queryFn: ({ signal }) => request<{ items: Site[] }>("/api/v1/orders/sites", { signal }),
    staleTime: 60_000,
  });

/** What a delivery write sends of the delivery as the page read it. */
export interface DeliveryRead {
  version: number;
  updatedAt: string;
}
const token = (read: DeliveryRead) => ({ version: read.version, expectedUpdatedAt: read.updatedAt });

export const createDelivery = (orderId: number) => send<Delivery>(base, json("POST", { orderId }));
export const updateDelivery = (id: number, read: DeliveryRead, lines: DeliveryLineInput[]) =>
  send<Delivery>(`${base}/${id}`, json("PUT", { ...token(read), lines }));
export const postDelivery = (id: number, read: DeliveryRead) =>
  send<Delivery>(`${base}/${id}/post`, json("POST", token(read)));
export const cancelDelivery = (id: number, read: DeliveryRead) =>
  send<Delivery>(`${base}/${id}/cancel`, json("POST", token(read)));

/** The pick list, printed now (plukkliste), and the packing slip, stored once posted (pakkseddel). */
export const pickListUrl = (id: number) => `${base}/${id}/pick-list`;
export const packingSlipUrl = (id: number) => `${base}/${id}/packing-slip`;
