import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { appUrl } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { handleUnauthorized, INVENTORY_QUERY_KEY, json, queryString, refusalOf, request, send } from "./request";

type Schemas = components["schemas"];

export type InventoryTransfer = Schemas["InventoryTransfer"];
export type InventoryTransferSummary = Schemas["InventoryTransferSummary"];
export type InventoryTransferLine = Schemas["InventoryTransferLine"];
export type InventoryTransferAction = Schemas["InventoryTransferAction"];
export type InventoryTransferStatus = Schemas["InventoryTransferStatus"];
export type InventoryTransferStatusFilter = Schemas["InventoryTransferStatusFilter"];
export type InventoryTransferInput = Schemas["InventoryTransferInput"];
export type InventoryTransferLineInput = Schemas["InventoryTransferLineInput"];
export type InventoryTransferSendInput = Schemas["InventoryTransferSendInput"];
export type InventoryTransferReceiveInput = Schemas["InventoryTransferReceiveInput"];
export type InventoryTransferReceiveLine = Schemas["InventoryTransferReceiveLine"];
export type InventoryTransferCloseReason = Schemas["InventoryTransferCloseReason"];
export type InventoryTransferSuggestion = Schemas["InventoryTransferSuggestion"];
export type InventoryStockAtSite = Schemas["InventoryStockAtSite"];

/** A transfer's statuses (phase 2 design I36), pinned to the contract by i18n.test.ts. */
export const transferStatuses: readonly InventoryTransferStatus[] = [
  "draft",
  "sent",
  "partly_received",
  "received",
  "cancelled",
];

/** Why what was not received is missing (I42), pinned to the contract by i18n.test.ts. */
export const closeReasons: readonly InventoryTransferCloseReason[] = ["lost", "damaged", "not_packed", "other"];

/** `GET /stock/sites` answers at most this many variants at a time (I53). */
export const MAX_STOCK_AT_SITES_VARIANTS = 100;

export interface TransfersFilter {
  status?: InventoryTransferStatusFilter;
  fromSiteId?: number;
  toSiteId?: number;
  variantId?: number;
  withMissing?: boolean;
  q?: string;
}

/** The transfers newest first, a page at a time by the server's cursor (I36). */
export const transfersInfiniteQueryOptions = (filter: TransfersFilter = {}) =>
  infiniteQueryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "transfers", filter],
    queryFn: ({ signal, pageParam }) =>
      request<Schemas["InventoryTransferPage"]>(
        `/api/v1/inventory/transfers${queryString({ ...filter, cursor: pageParam })}`,
        { signal },
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next,
  });

/** One transfer with its lines, their running totals, and its actions (I38, I46). */
export const transferQueryOptions = (transferId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "transfer", transferId],
    queryFn: ({ signal }) => request<InventoryTransfer>(`/api/v1/inventory/transfers/${transferId}`, { signal }),
  });

/** Variants' stock at sites (I53): on hand, reserved, available, incoming and the locations holding it, most first. */
export const stockAtSitesQueryOptions = (variantIds: readonly number[], siteIds: readonly number[]) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "stock-at-sites", variantIds, siteIds],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryStockAtSites"]>(
        `/api/v1/inventory/stock/sites${queryString({ variantIds: variantIds.join(","), siteIds: siteIds.join(",") })}`,
        { signal },
      ).then((answer) => answer.stock),
  });

// The writes go through `send`, so a refusal keeps its code and its failing lines.
export const createTransfer = (input: InventoryTransferInput) =>
  send<InventoryTransfer>("/api/v1/inventory/transfers", json("POST", input));

/** Replaces a draft's header and lines, guarded by the revision it was read at (I37). */
export const putTransfer = (transferId: number, input: InventoryTransferInput, revision: number) =>
  send<InventoryTransfer>(`/api/v1/inventory/transfers/${transferId}`, json("PUT", input, revision));

export const deleteTransfer = (transferId: number, revision: number) =>
  send<void>(`/api/v1/inventory/transfers/${transferId}`, {
    method: "DELETE",
    headers: { "If-Match": String(revision) },
  });

/** Sends a draft (I39), and with `receive` receives everything sent in the same step (I43). */
export const sendTransfer = (transferId: number, input: InventoryTransferSendInput, revision: number) =>
  send<InventoryTransfer>(`/api/v1/inventory/transfers/${transferId}/send`, json("POST", input, revision));

/** Receives what arrived now (I40), and with `close` records the rest as missing (I42). No If-Match (I41). */
export const receiveTransfer = (transferId: number, input: InventoryTransferReceiveInput) =>
  send<InventoryTransfer>(`/api/v1/inventory/transfers/${transferId}/receive`, json("POST", input));

export const cancelTransfer = (transferId: number, clientRequestId: string, revision: number) =>
  send<InventoryTransfer>(
    `/api/v1/inventory/transfers/${transferId}/cancel`,
    json("POST", { clientRequestId }, revision),
  );

/** The følgeseddel (I51), a PDF of a transfer sent or later. */
export const printTransferNote = async (transferId: number): Promise<Blob> => {
  const response = await fetch(appUrl(`/api/v1/inventory/transfers/${transferId}/note.pdf`), {
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return response.blob();
};
