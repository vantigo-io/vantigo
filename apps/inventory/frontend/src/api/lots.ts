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
export type InventoryLotRef = Schemas["InventoryLotRef"];
export type InventoryLotExpiringPage = Schemas["InventoryLotExpiringPage"];
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

/**
 * `POST /lots/{id}/holds/{holdId}/release`: one of Inventory's own holds, with why; with `downstream`, the lot
 * page's block also releases the blocks it put on the lots made from the lot (manufacturing phase 2 M92).
 */
export const releaseLotHold = (lotId: number, holdId: number, note: string, downstream = false) =>
  send<InventoryLot>(
    `/api/v1/inventory/lots/${lotId}/holds/${holdId}/release`,
    json("POST", { ...(note.trim() ? { note: note.trim() } : {}), ...(downstream ? { downstream } : {}) }),
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

/** What FEFO orders a lot by (M65): its expiry, when it was first received, and its id. */
interface FefoKeys {
  expiry?: string | null;
  receivedAt: string;
}

/**
 * Lots in FEFO order (manufacturing phase 2 M65), as the server's own
 * proposal takes them: expiry first, none last, then first received, then
 * the id — a list row's `id`, a stock row's `lotId`.
 */
export const fefoOrder = <T extends FefoKeys>(lots: readonly T[], idOf: (lot: T) => number) =>
  [...lots].sort(
    (a, b) =>
      (a.expiry ?? "9999-12-31").localeCompare(b.expiry ?? "9999-12-31") ||
      Date.parse(a.receivedAt) - Date.parse(b.receivedAt) ||
      idOf(a) - idOf(b),
  );

/** Whether a lot's stock can be taken: no hold and not expired (M63). */
export const usableLot = (lot: Pick<InventoryLotSummary, "status" | "expired">) =>
  lot.status === "available" && !lot.expired;

/** The expiring list's filter (M72): within how many days, at which site, of which status. */
export interface ExpiringFilter {
  days?: number;
  siteId?: number;
  status?: InventoryLotStatus;
}

/** The expiring list's default reach, in days, as the server's. */
export const DEFAULT_EXPIRING_DAYS = 30;

/** The lots expiring soon (manufacturing phase 2 M72): stock expiring within the days or already expired. */
export const expiringLotsQueryOptions = (filter: ExpiringFilter = {}, live = false) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "lots", "expiring", filter],
    queryFn: ({ signal }) =>
      request<InventoryLotExpiringPage>(`/api/v1/inventory/lots/expiring${queryString({ ...filter })}`, { signal }),
    refetchInterval: live ? false : LOTS_POLL_MS,
  });

/** The expiring list as CSV, with the list's filter. */
export const exportExpiringLots = async (filter: ExpiringFilter): Promise<Blob> => {
  const response = await fetch(appUrl(`/api/v1/inventory/lots/expiring.csv${queryString({ ...filter })}`), {
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return response.blob();
};

/** A lot label's format: the internal LOT token (M73), or GS1-128 (M93). */
export type InventoryLabelFormat = Schemas["InventoryLabelFormat"];

/**
 * A PDF of lot labels (M73), in the order of the ids: each the lot's LOT token as a Code 128 barcode, its
 * number, its item and its expiry — or, as `gs1`, a GS1-128 label with the variant's GTIN, the dates and the
 * lot (M93). Copies of each, at most 1000 labels in all.
 */
export const printLotLabels = async (
  ids: readonly number[],
  layout: "l7160" | "l7163",
  copies = 1,
  format: InventoryLabelFormat = "internal",
): Promise<Blob> => {
  const response = await fetch(
    appUrl(
      `/api/v1/inventory/lots/labels.pdf${queryString({ ids: ids.join(","), layout, copies, format: format === "gs1" ? format : undefined })}`,
    ),
    { credentials: "include" },
  );
  if (response.status === 401) await handleUnauthorized();
  if (!response.ok) throw await refusalOf(response);
  return response.blob();
};
