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
