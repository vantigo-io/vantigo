import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { INVENTORY_QUERY_KEY, json, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type InventoryMovement = Schemas["InventoryMovement"];
export type InventoryMovementSummary = Schemas["InventoryMovementSummary"];
export type InventoryMovementLine = Schemas["InventoryMovementLine"];
export type InventoryMovementKind = Schemas["InventoryMovementKind"];
export type InventoryAdjustmentReason = Schemas["InventoryAdjustmentReason"];
export type InventoryAdjustmentInput = Schemas["InventoryAdjustmentInput"];
export type InventoryAdjustmentMode = Schemas["InventoryAdjustmentLine"]["mode"];
export type InventoryMoveInput = Schemas["InventoryMoveInput"];
export type InventoryReceiptInput = Schemas["InventoryReceiptInput"];

/** The movement kinds of this phase (I8), pinned to the contract by i18n.test.ts. */
export const movementKinds: readonly InventoryMovementKind[] = [
  "adjustment",
  "move",
  "receipt",
  "count",
  "issue",
  "transfer_out",
  "transfer_in",
  "production",
];
/** An adjustment's reasons (I8). */
export const adjustmentReasons: readonly InventoryAdjustmentReason[] = [
  "correction",
  "damaged",
  "lost",
  "found",
  "opening_balance",
  "other",
];
/** An adjustment line's modes (I15): a signed change, or the new on hand. */
export const adjustmentModes: readonly InventoryAdjustmentMode[] = ["change", "set"];

/**
 * The refusal codes the module answers (design §4, as the contract's
 * InventoryProblem lists them): each has a `refusal.<code>` label, and a
 * posting's failing lines are marked with the same words.
 */
export const refusalCodes = [
  "site_code_taken",
  "location_code_taken",
  "full_code_taken",
  "code_invalid",
  "depth_exceeded",
  "parent_invalid",
  "bulk_too_large",
  "location_has_stock",
  "location_has_history",
  "root_location",
  "revision_conflict",
  "insufficient_stock",
  "client_request_reused",
  "location_archived",
  "location_not_found",
  "variant_not_found",
  "variant_not_stockable",
  "variant_discontinued",
  "move_crosses_sites",
  "same_location",
  "quantity_invalid",
  "nothing_to_post",
  "too_many_lines",
  "dependency_unavailable",
  // Delivery B: counts, labels and quantities entered in packagings.
  "count_scope_overlaps",
  "count_not_open",
  "count_lines_moved",
  "location_has_open_count",
  "labels_too_many",
  "packaging_not_found",
  "entered_invalid",
  // Phase 2, delivery A: transfers between sites.
  "transfer_same_site",
  "site_archived",
  "transfer_not_draft",
  "transfer_not_receivable",
  "transfer_not_cancellable",
  "transfer_over_received",
  "from_location_required",
  "transfer_line_unknown",
  "nothing_to_close",
  "receive_incomplete",
  "site_has_open_transfers",
  "location_in_transit",
  "site_has_transfers",
  // Phase 2, delivery B: placements.
  "maximum_below_reorder_point",
  "placement_empty",
  "placement_location_invalid",
] as const;

export interface MovementsFilter {
  siteId?: number;
  locationId?: number;
  variantId?: number;
  kind?: InventoryMovementKind;
  from?: string;
  to?: string;
}

/** The movements newest first, a page of `limit` at a time by the server's cursor (I30). */
export const movementsInfiniteQueryOptions = (filter: MovementsFilter = {}, limit = 100) =>
  infiniteQueryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "movements", filter, limit],
    queryFn: ({ signal, pageParam }) =>
      request<Schemas["InventoryMovementPage"]>(
        `/api/v1/inventory/movements${queryString({ ...filter, limit, cursor: pageParam })}`,
        { signal },
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  });

export const movementQueryOptions = (movementId: number) =>
  queryOptions({
    queryKey: [INVENTORY_QUERY_KEY, "movement", movementId],
    queryFn: ({ signal }) => request<InventoryMovement>(`/api/v1/inventory/movements/${movementId}`, { signal }),
  });

// The postings go through `send`, so a refusal keeps its code and its failing lines.
export const postAdjustment = (input: InventoryAdjustmentInput) =>
  send<InventoryMovement>("/api/v1/inventory/movements/adjustments", json("POST", input));

export const postMove = (input: InventoryMoveInput) =>
  send<InventoryMovement>("/api/v1/inventory/movements/moves", json("POST", input));

export const postReceipt = (input: InventoryReceiptInput) =>
  send<InventoryMovement>("/api/v1/inventory/movements/receipts", json("POST", input));
