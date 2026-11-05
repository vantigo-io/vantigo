import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The From orders wizard (orders design O37, O39): the orders with lines left
 * to invoice, as Orders answers them through this module, and the drafts the
 * wizard makes of the lines chosen.
 */
export type DueOrdersView = Schemas["InvoicesDueOrdersResponse"];
export type DueOrder = Schemas["InvoicesDueOrder"];
export type DueOrderLine = Schemas["InvoicesDueOrderLine"];
export type FromOrdersInput = Schemas["InvoicesFromOrdersRequest"];
export type FromOrdersResult = Schemas["InvoicesFromOrdersResponse"];
export type FromOrdersDraft = Schemas["InvoicesFromOrdersDraft"];

/** The kind a line source names an order line's allocation by on the wire. */
export const ALLOCATION_KIND = "orders.allocation";

/** Which orders the due list is asked for: customers, or orders — never both. */
export interface DueOrdersQuery {
  customerIds?: number[];
  orderIds?: number[];
}

const queryString = ({ customerIds = [], orderIds = [] }: DueOrdersQuery) =>
  [...customerIds.map((id) => `customerId=${id}`), ...orderIds.map((id) => `orderId=${id}`)].join("&");

/** GET /invoices/from-orders/due: the orders named, with their lines left to invoice. */
export const getDueOrders = (query: DueOrdersQuery, signal?: AbortSignal): Promise<DueOrdersView> =>
  request<DueOrdersView>(`/api/v1/invoices/from-orders/due?${queryString(query)}`, { signal });

export const dueOrdersQueryOptions = (query: DueOrdersQuery) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "from-orders", "due", query.customerIds ?? [], query.orderIds ?? []],
    queryFn: ({ signal }) => getDueOrders(query, signal),
  });

/**
 * POST /invoices/from-orders: the drafts made of the lines at the quantities
 * asked — 201. The caller invalidates every invoices query.
 */
export const postFromOrders = (input: FromOrdersInput): Promise<FromOrdersResult> =>
  request<FromOrdersResult>("/api/v1/invoices/from-orders", json("POST", input));
