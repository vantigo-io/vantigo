import type {
  InventoryAdjustmentInput,
  InventoryAdjustmentMode,
  InventoryAdjustmentReason,
  InventoryMoveInput,
  InventoryReceiptInput,
} from "../api/movements";
import type { InventoryVariantRef } from "../api/stock";
import type { InventoryTransferLine, InventoryTransferLineInput, InventoryTransferSuggestion } from "../api/transfers";
import type { components } from "../api-schema";
import { packedQuantity } from "./breakdown";

type InventoryEntered = components["schemas"]["InventoryEntered"];

/** The three records the line editor writes (I15); B's count joins them. */
export type LineEditorKind = "adjustment" | "move" | "receipt";

/** What the line editor's rows are for: the three postings, or a transfer's draft, whose lines name no location (phase 2 design I36). */
export type EditorKind = LineEditorKind | "transfer";

/** One row of the editor, as the person is filling it in. */
export interface EditorLine {
  /** Stable across edits, so a refusal's marks stay on their row while rows above it change. */
  key: string;
  variant?: InventoryVariantRef;
  /** The quantity in the variant's unit; with packagings counted, the loose rest beside them. */
  quantity: number | "";
  /** How many of each of the variant's packagings, by packaging id (I25); none counted sends `quantity` alone. */
  packs: Readonly<Record<number, number>>;
  /**
   * An adjustment's change of a variant with packagings takes stock away
   * rather than adding it: entered quantities are never negative, so the
   * direction is said apart (`decrease`).
   */
  decrease: boolean;
  /** The adjustment's location, the move's from, or the receipt's override of its default location. */
  locationId: number | null;
  /** The move's to. */
  toLocationId: number | null;
  /** The adjustment's mode: a signed change, or the new on hand. */
  mode: InventoryAdjustmentMode;
  /** A transfer line drafted from the low-stock list keeps its suggestion until the line is edited (I57). */
  suggestion?: InventoryTransferSuggestion;
  /**
   * A tracked variant's lot (manufacturing phase 2 M59): the number typed or
   * picked — on a receipt, empty makes one (`L<n>`) — and, when it names a
   * lot the variant has at the site, its id; a move names only an existing
   * lot.
   */
  lotNumber: string;
  lotId?: number;
  /** A new lot's dates on a receipt or an increase, as `YYYY-MM-DD`; empty ones default on the server (M57). */
  lotDates: LotDates;
  /** A receipt of a serial variant: the first serial number and how many, counted up (M57). */
  serialFirst: string;
  serialCount: number | "";
}

/** A new lot's dates as the person typed them; empty is left out. */
export interface LotDates {
  productionDate: string;
  bestBefore: string;
  expiry: string;
}

const noDates: LotDates = { productionDate: "", bestBefore: "", expiry: "" };

/** Whether the variant's stock is kept in lots or serials (M49): its lines name a lot. */
export const isTracked = (variant: InventoryVariantRef | undefined) =>
  variant?.tracking === "lot" || variant?.tracking === "serial";

/** Whether a receipt line takes serial numbers rather than one lot. */
export const takesSerials = (kind: EditorKind, variant: InventoryVariantRef | undefined) =>
  kind === "receipt" && variant?.tracking === "serial";

let nextKey = 0;

/**
 * A quantity at the three decimals it is stored with (I11). Sums in binary
 * floating point drift — 0.118 + 1 is 1.1179999999999999 — and the server
 * refuses a quantity with more than three decimals, so every quantity the
 * editor works out, and every one it sends, goes through here.
 */
export const roundQuantity = (n: number): number => Math.round(n * 1000) / 1000;

/**
 * A new row: the variant scanned or picked, one of it — or one of the
 * packaging whose barcode was scanned (I25) — on the editor's default location.
 */
export const newEditorLine = (
  kind: EditorKind,
  defaultLocationId: number | null,
  variant?: InventoryVariantRef,
  packagingId?: number,
): EditorLine => ({
  key: `line-${++nextKey}`,
  variant,
  quantity: variant && packagingId === undefined ? 1 : "",
  packs: packagingId === undefined ? {} : { [packagingId]: 1 },
  decrease: false,
  // A receipt line names a location only to override the receipt's own; a transfer's names none until it is sent.
  locationId: kind === "receipt" || kind === "transfer" ? null : defaultLocationId,
  toLocationId: null,
  mode: "change",
  lotNumber: "",
  lotDates: noDates,
  serialFirst: "",
  serialCount: "",
});

/** Whether any of the row's packagings is counted, so it is sent as `entered` (I25). */
export const isPacked = (line: Pick<EditorLine, "packs">) => Object.values(line.packs).some((count) => count > 0);

/** Whether the row's direction is its Add / Remove choice rather than the quantity's sign. */
export const usesDecrease = (kind: EditorKind, line: Pick<EditorLine, "variant" | "mode">) =>
  kind === "adjustment" && line.mode === "change" && (line.variant?.packagings?.length ?? 0) > 0;

/**
 * What the row stands for in the variant's unit: the packagings counted and
 * the loose rest, or the quantity typed; negative when it takes stock away.
 * Empty while nothing is filled in.
 */
export const lineQuantity = (kind: EditorKind, line: EditorLine): number | "" => {
  const total = isPacked(line)
    ? packedQuantity(line.variant?.packagings ?? [], line.packs, Number(line.quantity || 0))
    : line.quantity;
  if (total === "") return "";
  return usesDecrease(kind, line) && line.decrease ? -total : total;
};

/** The packagings counted and the loose rest, as a line sends them (I25). */
export const enteredOf = (packs: Readonly<Record<number, number>>, loose: number | ""): InventoryEntered => ({
  packagings: Object.entries(packs)
    .filter(([, count]) => count > 0)
    .map(([packagingId, count]) => ({ packagingId: Number(packagingId), count })),
  ...(loose === "" || loose === 0 ? {} : { loose: roundQuantity(loose) }),
});

/**
 * The row's quantity as the contract takes it: `entered` when a packaging is
 * counted, else `quantity` — exactly one of the two (I25). An adjustment's
 * change entered in packagings takes away with `decrease`.
 */
const amountOf = (kind: EditorKind, line: EditorLine) => {
  if (!isPacked(line)) {
    const quantity = roundQuantity(Number(line.quantity));
    return { quantity: usesDecrease(kind, line) && line.decrease ? -quantity : quantity };
  }
  return {
    entered: enteredOf(line.packs, line.quantity),
    ...(usesDecrease(kind, line) && line.decrease ? { decrease: true } : {}),
  };
};

/** What the person must still fill in on a row before it can be sent; undefined when it is complete. */
export const lineGap = (
  kind: EditorKind,
  line: EditorLine,
): "variant" | "quantity" | "location" | "lot" | undefined => {
  if (!line.variant) return "variant";
  const quantity = lineQuantity(kind, line);
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
  if (kind !== "transfer" && isTracked(line.variant)) {
    if (takesSerials(kind, line.variant)) {
      if (!line.serialFirst.trim() || line.serialCount === "" || line.serialCount < 1) return "lot";
    } else if (kind === "move" ? line.lotId === undefined : kind === "adjustment" && !line.lotNumber.trim())
      return "lot";
  }
  return undefined;
};

/** The dates the person gave, as a new lot sends them. */
const datesOf = (dates: LotDates) => ({
  ...(dates.productionDate ? { productionDate: dates.productionDate } : {}),
  ...(dates.bestBefore ? { bestBefore: dates.bestBefore } : {}),
  ...(dates.expiry ? { expiry: dates.expiry } : {}),
});

/**
 * A tracked row's lot as the contract takes it (M57, M59): a receipt names
 * its lot by number — an empty one is made, an existing one is added to —
 * with the dates given, or its serials; a move names the lot's id; an
 * adjustment the id of a lot the variant has, or a new one by number. An
 * untracked row names none.
 */
const lotOf = (kind: EditorKind, line: EditorLine) => {
  if (kind === "transfer" || !isTracked(line.variant)) return {};
  if (takesSerials(kind, line.variant)) {
    const dates = datesOf(line.lotDates);
    return {
      serials: { first: line.serialFirst.trim(), count: Number(line.serialCount) },
      ...(Object.keys(dates).length > 0 ? { lot: dates } : {}),
    };
  }
  if (kind === "move" || (kind === "adjustment" && line.lotId !== undefined)) return { lotId: line.lotId };
  const number = line.lotNumber.trim();
  return { lot: { ...(number ? { number } : {}), ...datesOf(line.lotDates) } };
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
    ...amountOf("adjustment", line),
    ...lotOf("adjustment", line),
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
    ...amountOf("move", line),
    ...lotOf("move", line),
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
    ...amountOf("receipt", line),
    ...lotOf("receipt", line),
    ...(line.locationId === null || line.locationId === header.toLocationId ? {} : { locationId: line.locationId }),
  })),
});

/** A transfer draft's lines (I36): each a variant and its quantity or packagings, and the suggestion it still keeps. */
export const transferLinesBody = (lines: readonly EditorLine[]): InventoryTransferLineInput[] =>
  lines.map((line) => ({
    variantId: line.variant?.id ?? 0,
    ...amountOf("transfer", line),
    // A tracked line may name its lot on the draft (M70); else the send proposes one.
    ...(isTracked(line.variant) && line.lotId !== undefined ? { lotId: line.lotId } : {}),
    ...(line.suggestion ? { suggestion: line.suggestion } : {}),
  }));

/** A saved draft's lines as the editor's rows, packagings as they were entered (I25). */
export const editorLinesOf = (lines: readonly InventoryTransferLine[]): EditorLine[] =>
  lines.map((line) => {
    const row = {
      ...newEditorLine("transfer", null, line.variant),
      ...(line.lot ? { lotId: line.lot.id, lotNumber: line.lot.number } : {}),
    };
    if (!line.entered) return { ...row, quantity: line.quantity, suggestion: line.suggestion };
    return {
      ...row,
      quantity: line.entered.loose ?? "",
      packs: Object.fromEntries(line.entered.packagings.map((one) => [one.packagingId, one.count])),
      suggestion: line.suggestion,
    };
  });
