import type { Line, LineInput, LineKind, TemplateLine } from "../api/types";
import type { components } from "../api-schema";

type TemplateLineInput = components["schemas"]["OrdersTemplateLineInput"];

let nextKey = 0;
/** A key a row keeps while it is moved, before and after it has an id. */
const freshKey = () => `row-${++nextKey}`;

/**
 * A line as the editor holds it (O15). The entry fields are what the person
 * typed; `saved` is the server's line it came from, whose amounts are the
 * only ones shown (O17) — a row changed since the last save shows none until
 * it is saved again.
 */
export interface EditableLine {
  key: string;
  id?: number;
  kind: LineKind;
  /** A catalog line names a variant, or will once one is picked; a free-text line never does (O15). */
  catalog: boolean;
  variantId: number | null;
  sku: string | null;
  /** A free-text line's name, or the catalog name shown for a variant's. */
  name: string;
  /** The line's text; a section's heading or a note's words. */
  description: string;
  quantity: number | "";
  unit: string;
  /** The unit price as entered: including VAT when the document's prices include VAT (O19). */
  unitPrice: number | "";
  /** Whether the person typed the price, which makes it manual (O16). */
  priceTouched: boolean;
  discountPercent: number | "";
  vatCodeId: number | null;
  optional: boolean;
  saved?: Line;
  /** Whether any entry field moved since `saved`. */
  changed: boolean;
}

/** A line the server answered, as an editor row. */
export const fromLine = (line: Line): EditableLine => ({
  key: `line-${line.id}`,
  id: line.id,
  kind: line.kind,
  catalog: line.variantId != null,
  variantId: line.variantId ?? null,
  sku: line.sku ?? null,
  name: line.name ?? "",
  description: line.description,
  quantity: line.quantity ?? "",
  unit: line.unit,
  unitPrice: line.displayUnitPrice ?? line.unitPrice ?? "",
  priceTouched: false,
  discountPercent: line.discountPercent,
  vatCodeId: line.vatCodeId ?? null,
  optional: line.optional,
  saved: line,
  changed: false,
});

/** A template's line as an editor row: no amounts, a price override when it has one. */
export const fromTemplateLine = (line: TemplateLine): EditableLine => ({
  key: `template-line-${line.id}`,
  id: line.id,
  kind: line.kind,
  catalog: line.variantId != null,
  variantId: line.variantId ?? null,
  sku: line.sku ?? null,
  name: line.name ?? "",
  description: line.description,
  quantity: line.quantity ?? "",
  unit: "",
  unitPrice: line.unitPrice ?? "",
  priceTouched: false,
  discountPercent: 0,
  vatCodeId: null,
  optional: line.optional,
  changed: false,
});

/** A new, empty row of a kind: a catalog line to pick a variant for, a free-text line, a section or a note. */
export const newLine = (kind: LineKind, catalog = false): EditableLine => ({
  key: freshKey(),
  kind,
  catalog: kind === "product" && catalog,
  variantId: null,
  sku: null,
  name: "",
  description: "",
  quantity: kind === "product" ? 1 : "",
  unit: kind === "product" ? "stk" : "",
  unitPrice: "",
  priceTouched: false,
  discountPercent: 0,
  vatCodeId: null,
  optional: false,
  changed: true,
});

/** A copy of a row with some fields changed, marked as changed since the last save. */
export const editLine = (line: EditableLine, patch: Partial<EditableLine>): EditableLine => ({
  ...line,
  ...patch,
  changed: true,
});

/**
 * A row as the save sends it (OrdersLineInput). A price is sent only when the
 * person typed it — or entered one on a new row — so an untouched catalog
 * price is re-priced by the server and an untouched manual one is kept
 * ("absent keeps or takes the catalog price"). The optional flag is a
 * quote's only (O21).
 */
export const toLineInput = (line: EditableLine, { quote }: { quote: boolean }): LineInput => {
  if (line.kind !== "product")
    return { ...(line.id ? { id: line.id } : {}), kind: line.kind, description: line.description };
  const priced = line.unitPrice !== "" && (line.priceTouched || line.id === undefined);
  return {
    ...(line.id ? { id: line.id } : {}),
    kind: "product",
    variantId: line.variantId,
    ...(line.catalog ? {} : { name: line.name, vatCodeId: line.vatCodeId }),
    description: line.description,
    quantity: line.quantity === "" ? 0 : line.quantity,
    unit: line.unit,
    ...(priced ? { unitPrice: line.unitPrice as number } : {}),
    discountPercent: line.discountPercent === "" ? 0 : line.discountPercent,
    ...(quote ? { optional: line.optional } : {}),
  };
};

/** Moves the row at `from` one step up (-1) or down (+1). */
export const moveLine = (lines: readonly EditableLine[], from: number, step: -1 | 1): EditableLine[] => {
  const to = from + step;
  if (to < 0 || to >= lines.length) return [...lines];
  const next = [...lines];
  [next[from], next[to]] = [next[to], next[from]];
  return next;
};

/** An order line's open quantity: ordered less cancelled less delivered, never below 0 (O32). */
export const remainingToDeliver = (line: Line): number =>
  Math.max(0, Math.round(((line.quantity ?? 0) - line.qtyCancelled - line.qtyDelivered) * 1000) / 1000);

/** A template's row as its save sends it (OrdersTemplateLineInput): a price only as an override. */
export const toTemplateLineInput = (line: EditableLine): TemplateLineInput => {
  if (line.kind !== "product")
    return { ...(line.id ? { id: line.id } : {}), kind: line.kind, description: line.description };
  return {
    ...(line.id ? { id: line.id } : {}),
    kind: "product",
    variantId: line.variantId,
    ...(line.catalog ? {} : { name: line.name }),
    description: line.description,
    quantity: line.quantity === "" ? null : line.quantity,
    unitPrice: line.unitPrice === "" ? null : line.unitPrice,
    optional: line.optional,
  };
};

/**
 * The rows a save would send incomplete, by index, with the catalog key of
 * what is missing: a catalog row with no variant picked, a free-text row
 * with no name, a product row with no quantity. The server would refuse
 * them; the editor says so on the row before it sends anything.
 */
export const incompleteLines = (lines: readonly EditableLine[], { template = false } = {}): Map<number, string> => {
  const problems = new Map<number, string>();
  lines.forEach((line, index) => {
    if (line.kind !== "product") return;
    if (line.catalog && line.variantId === null) problems.set(index, "linePickVariant");
    else if (!line.catalog && line.name.trim() === "") problems.set(index, "lineNameMissing");
    else if (!template && (line.quantity === "" || line.quantity <= 0)) problems.set(index, "lineQuantityMissing");
  });
  return problems;
};
