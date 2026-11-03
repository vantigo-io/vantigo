import type { InventoryMeta } from "../api/meta";
import type { LineEditorKind } from "./lines";

/**
 * Whether the caller may post a movement of this kind (design §5): every
 * stock write needs `inventory:locations-view` and `inventory:stock-view`
 * besides its own key — `inventory:receipts-manage` for a receipt,
 * `inventory:stock-adjust` for an adjustment or a move — so a control that
 * offers one checks all three, as the server will.
 */
export const canPost = (meta: InventoryMeta | undefined, kind: LineEditorKind): boolean =>
  !!meta && meta.canViewLocations && meta.canViewStock && (kind === "receipt" ? meta.canReceive : meta.canAdjust);

/** Whether the caller may open, count, post and cancel counts: `inventory:counts-manage` and both views (design §5). */
export const canCount = (meta: InventoryMeta | undefined): boolean =>
  !!meta && meta.canViewLocations && meta.canViewStock && meta.canCount;

/** Whether the caller may import opening balances: `inventory:stock-adjust` and both views (I32). */
export const canImport = (meta: InventoryMeta | undefined): boolean => canPost(meta, "adjustment");

/** Whether the caller may print location labels: `inventory:locations-view` alone (I7). */
export const canPrintLabels = (meta: InventoryMeta | undefined): boolean => !!meta?.canViewLocations;
