import { infiniteQueryOptions, queryOptions, useQueryClient } from "@tanstack/react-query";
import { appUrl, useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { handleUnauthorized, INVENTORY_QUERY_KEY, json, queryString, refusalOf, request, send } from "./request";

type Schemas = components["schemas"];

export type InventoryLot = Schemas["InventoryLot"];
export type InventoryLotSummary = Schemas["InventoryLotSummary"];
export type InventoryLotStatus = Schemas["InventoryLotStatus"];
export type InventoryLotHold = Schemas["InventoryLotHold"];
export type InventoryLotHoldInput = Schemas["InventoryLotHoldInput"];
export type InventoryLotUpdate = Schemas["InventoryLotUpdate"];
export type InventoryLotAttachment = Schemas["InventoryLotAttachment"];
export type InventoryBalanceLot = Schemas["InventoryBalanceLot"];
export type InventoryLotHoldCause = InventoryLotHoldInput["cause"];
export type InventoryLotHoldKind = InventoryLotHoldInput["kind"];

/** A lot's statuses, derived from its holds (manufacturing phase 2 M64), pinned to the contract by i18n.test.ts. */
export const lotStatuses: readonly InventoryLotStatus[] = ["available", "quarantine", "blocked"];
/** A hold's causes (M64). */
export const holdCauses: readonly InventoryLotHoldCause[] = ["inspection", "deviation", "complaint", "recall", "other"];
/** A hold's kinds (M64). */
export const holdKinds: readonly InventoryLotHoldKind[] = ["quarantine", "block"];

/** A lot status's badge colour. */
export const lotStatusColor = { available: "teal", quarantine: "orange", blocked: "red" } as const;

/** The server's own bounds on a lot's files (M54): checked here so a file is not sent to be refused. */
export const MAX_LOT_FILE_BYTES = 20 * 1024 * 1024;
export const MAX_LOT_FILES = 20;
export const LOT_FILE_TYPES = ["application/pdf", "image/jpeg", "image/png"] as const;

/** How often a lot list or page is read again while its live topic is down. */
export const LOTS_POLL_MS = 30_000;

export interface LotsFilter {
  q?: string;
  variantId?: number;
  siteId?: number;
  status?: InventoryLotStatus;
  holdCause?: InventoryLotHoldCause;
  expiresBefore?: string;
  /** Only lots holding more than 0 — at the site when one is named; the server's default is true. */
  withStock?: boolean;
}

/** The lots list (M60), by variant then number, a page of `limit` at a time; polled while not live. */
export const lotsInfiniteQueryOptions = (filter: LotsFilter = {}, live = false, limit = 100) =>
  infiniteQueryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "lots", filter, limit],
    queryFn: ({ signal, pageParam }) =>
      request<Schemas["InventoryLotPage"]>(
        `/api/v1/inventory/lots${queryString({ ...filter, limit, cursor: pageParam })}`,
        {
          signal,
        },
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
    refetchInterval: live ? false : LOTS_POLL_MS,
  });

/** A variant's lots holding stock at a site, by number: what a form's lot picker offers. */
export const variantLotsQueryOptions = (variantId: number, siteId: number | undefined) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "lots", "picker", variantId, siteId],
    queryFn: ({ signal }) =>
      request<Schemas["InventoryLotPage"]>(
        `/api/v1/inventory/lots${queryString({ variantId, siteId, withStock: true, limit: 500 })}`,
        { signal },
      ).then((page) => page.lots),
    staleTime: 10_000,
  });

/** One lot's page (M61); polled while not live. */
export const lotQueryOptions = (lotId: number, live = false) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "lot", lotId],
    queryFn: ({ signal }) => request<InventoryLot>(`/api/v1/inventory/lots/${lotId}`, { signal }),
    refetchInterval: live ? false : LOTS_POLL_MS,
  });

/** `PUT /lots/{id}`: the supplier lot, the three dates and the note, replaced whole at the revision read. */
export const putLot = (lotId: number, input: InventoryLotUpdate, revision: number) =>
  send<InventoryLot>(`/api/v1/inventory/lots/${lotId}`, json("PUT", input, revision));

/** `POST /lots/{id}/holds`: a hold of the lot page. */
export const postLotHold = (lotId: number, input: InventoryLotHoldInput) =>
  send<InventoryLot>(`/api/v1/inventory/lots/${lotId}/holds`, json("POST", input));

/** `POST /lots/{id}/holds/{holdId}/release`: one of Inventory's own holds, with why. */
export const releaseLotHold = (lotId: number, holdId: number, note: string) =>
  send<InventoryLot>(
    `/api/v1/inventory/lots/${lotId}/holds/${holdId}/release`,
    json("POST", note.trim() ? { note: note.trim() } : {}),
  );

/** `POST /lots/{id}/attachments`: one file, as the part named `file`. */
export const uploadLotAttachment = async (lotId: number, file: File): Promise<InventoryLotAttachment> => {
  const form = new FormData();
  form.append("file", file, file.name);
  const response = await fetch(appUrl(`/api/v1/inventory/lots/${lotId}/attachments`), {
    method: "POST",
    body: form,
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (response.ok) return (await response.json()) as InventoryLotAttachment;
  throw await refusalOf(response);
};

/** `DELETE /lots/{id}/attachments/{attachmentId}`. */
export const deleteLotAttachment = (lotId: number, attachmentId: number) =>
  send<void>(`/api/v1/inventory/lots/${lotId}/attachments/${attachmentId}`, { method: "DELETE" });

/** Where a lot's file is read: the download streams it, under the session cookie. */
export const lotAttachmentHref = (lotId: number, attachmentId: number) =>
  appUrl(`/api/v1/inventory/lots/${lotId}/attachments/${attachmentId}`);

/**
 * Listens to the lots' live topics (manufacturing phase 2 §17) and refetches
 * on an event: the list's keyless `inventory.lots`, or one lot's
 * `inventory.lot:<id>`. Returns whether the topic is live: the screens poll
 * only while it is not.
 */
export function useLotsLive(lotId?: number): boolean {
  const client = useQueryClient();
  return useLiveTopic(lotId === undefined ? "inventory.lots" : `inventory.lot:${lotId}`, () => {
    void client.invalidateQueries({
      queryKey: lotId === undefined ? [INVENTORY_QUERY_KEY, "lots"] : [INVENTORY_QUERY_KEY, "lot", lotId],
    });
  });
}

/** A variant's lots in FEFO order (manufacturing phase 2 M65): expiry first, none last, then number. */
export const fefoOrder = (lots: readonly InventoryLotSummary[]) =>
  [...lots].sort(
    (a, b) =>
      (a.expiry ?? "9999-12-31").localeCompare(b.expiry ?? "9999-12-31") || a.number.localeCompare(b.number, "en"),
  );

/** Whether a lot's stock can be taken: no hold and not expired (M63). */
export const usableLot = (lot: InventoryLotSummary) => lot.status === "available" && !lot.expired;
