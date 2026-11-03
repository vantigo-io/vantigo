import type {
  InventoryAdjustmentInput,
  InventoryAdjustmentMode,
  InventoryAdjustmentReason,
  InventoryMoveInput,
  InventoryReceiptInput,
} from "../api/movements";
import type { InventoryVariantRef } from "../api/stock";

/** The three records the line editor writes (I15); B's count joins them. */
export type LineEditorKind = "adjustment" | "move" | "receipt";

/** One row of the editor, as the person is filling it in. */
export interface EditorLine {
  /** Stable across edits, so a refusal's marks stay on their row while rows above it change. */
  key: string;
  variant?: InventoryVariantRef;
  quantity: number | "";
  /** The adjustment's location, the move's from, or the receipt's override of its default location. */
  locationId: number | null;
  /** The move's to. */
  toLocationId: number | null;
  /** The adjustment's mode: a signed change, or the new on hand. */
  mode: InventoryAdjustmentMode;
}

let nextKey = 0;

/**
 * A quantity at the three decimals it is stored with (I11). Sums in binary
 * floating point drift — 0.118 + 1 is 1.1179999999999999 — and the server
 * refuses a quantity with more than three decimals, so every quantity the
 * editor works out, and every one it sends, goes through here.
 */
export const roundQuantity = (n: number): number => Math.round(n * 1000) / 1000;

/** A new row: the variant scanned or picked, one of it, on the editor's default location. */
export const newEditorLine = (
  kind: LineEditorKind,
  defaultLocationId: number | null,
  variant?: InventoryVariantRef,
): EditorLine => ({
  key: `line-${++nextKey}`,
  variant,
  quantity: variant ? 1 : "",
  // A receipt line names a location only to override the receipt's own.
  locationId: kind === "receipt" ? null : defaultLocationId,
  toLocationId: null,
  mode: "change",
});

/** What the person must still fill in on a row before it can be sent; undefined when it is complete. */
export const lineGap = (kind: LineEditorKind, line: EditorLine): "variant" | "quantity" | "location" | undefined => {
  if (!line.variant) return "variant";
  const quantity = line.quantity;
  if (quantity === "") return "quantity";
  if (
    kind === "adjustment" && line.mode === "change"
      ? quantity === 0
      : kind === "adjustment"
        ? quantity < 0
        : quantity <= 0
  )
    return "quantity";
  if (kind === "adjustment" && line.locationId === null) return "location";
  if (kind === "move" && (line.locationId === null || line.toLocationId === null)) return "location";
  return undefined;
};

/** The header of a posting, beside its lines. */
export interface PostingHeader {
  clientRequestId: string;
  siteId: number;
  note: string;
  reason: InventoryAdjustmentReason | null;
  /** The receipt's default location. */
  toLocationId: number | null;
  supplierName: string;
  supplierReference: string;
}

const optional = (text: string) => (text.trim() ? text.trim() : undefined);

/** An adjustment's body; every row complete (`lineGap`). */
export const adjustmentBody = (header: PostingHeader, lines: readonly EditorLine[]): InventoryAdjustmentInput => ({
  clientRequestId: header.clientRequestId,
  siteId: header.siteId,
  reason: header.reason ?? "correction",
  note: optional(header.note),
  lines: lines.map((line) => ({
    variantId: line.variant?.id ?? 0,
    locationId: line.locationId ?? 0,
    mode: line.mode,
    quantity: roundQuantity(Number(line.quantity)),
  })),
});

export const moveBody = (header: PostingHeader, lines: readonly EditorLine[]): InventoryMoveInput => ({
  clientRequestId: header.clientRequestId,
  siteId: header.siteId,
  note: optional(header.note),
  lines: lines.map((line) => ({
    variantId: line.variant?.id ?? 0,
    fromLocationId: line.locationId ?? 0,
    toLocationId: line.toLocationId ?? 0,
    quantity: roundQuantity(Number(line.quantity)),
  })),
});

/** A receipt's body: a row names its location only when it overrides the receipt's default. */
export const receiptBody = (header: PostingHeader, lines: readonly EditorLine[]): InventoryReceiptInput => ({
  clientRequestId: header.clientRequestId,
  siteId: header.siteId,
  toLocationId: header.toLocationId ?? 0,
  supplierName: optional(header.supplierName),
  supplierReference: optional(header.supplierReference),
  note: optional(header.note),
  lines: lines.map((line) => ({
    variantId: line.variant?.id ?? 0,
    quantity: roundQuantity(Number(line.quantity)),
    ...(line.locationId === null || line.locationId === header.toLocationId ? {} : { locationId: line.locationId }),
  })),
});
