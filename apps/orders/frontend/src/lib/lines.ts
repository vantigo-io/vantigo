import type { InvoicePolicy, Line, LineInput, LineKind, TemplateLine, VariantPackaging } from "../api/types";
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
  /**
   * The packagings the line may be entered in (O15): the picked variant's, or
   * the one a saved line was entered in. A line entered in one sends how many
   * of it, and the server stores the quantity in the variant's unit.
   */
  packagings: VariantPackaging[];
  packagingId: number | null;
  packagingQuantity: number | "";
  saved?: Line;
  /** Whether any entry field moved since `saved`. */
  changed: boolean;
  /**
   * An order line's invoice policy (O48), sent only when the person chose it:
   * the server keeps a kept line's and defaults a new one's otherwise.
   */
  invoicePolicy?: InvoicePolicy;
  policyTouched?: boolean;
  /**
   * A shipping line's price is the carrier's estimate with the markup (O53),
   * set by *Estimate* and cleared when a price is typed over it.
   */
  carrierPriced?: boolean;
}

/**
 * The packagings a saved line may be entered in (O15): the variant's current
 * ones, when they are known, and the one the line was saved in when the
 * catalog no longer has it — its size from the line, as the server keeps it.
 */
const savedPackagings = (line: Line, current: readonly VariantPackaging[] = []): VariantPackaging[] => {
  const saved =
    line.packagingId != null && line.packagingQuantity != null && line.quantity != null
      ? {
          id: line.packagingId,
          name: line.packagingName ?? "",
          quantity: Math.round((line.quantity / line.packagingQuantity) * 1000) / 1000,
        }
      : null;
  return saved && !current.some((p) => p.id === saved.id) ? [...current, saved] : [...current];
};

/** An order's child lines by their kit line's id (manufacturing phase 2 M145). */
export const kitChildrenOf = (lines: readonly Line[]): Map<number, Line[]> => {
  const out = new Map<number, Line[]>();
  for (const line of lines) {
    if (line.parentLineId == null) continue;
    out.set(line.parentLineId, [...(out.get(line.parentLineId) ?? []), line]);
  }
  return out;
};

/**
 * The name of the kit each child line is a component of, by the child's id
 * (manufacturing phase 2 M145): what tells a component apart from a line of
 * the same product on its own.
 */
export const kitNamesOf = (lines: readonly Line[]): Map<number, string> => {
  const byId = new Map(lines.map((line) => [line.id, line]));
  const out = new Map<number, string>();
  for (const line of lines) {
    const kit = line.parentLineId == null ? undefined : byId.get(line.parentLineId);
    if (kit) out.set(line.id, kit.name ?? kit.description);
  }
  return out;
};

/** A line the server answered, as an editor row, offered its variant's current packagings when they are known. */
export const fromLine = (line: Line, current?: readonly VariantPackaging[]): EditableLine => ({
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
  packagings: savedPackagings(line, current),
  packagingId: line.packagingId ?? null,
  packagingQuantity: line.packagingQuantity ?? "",
  saved: line,
  changed: false,
  invoicePolicy: line.invoicePolicy,
  policyTouched: false,
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
  packagings: [],
  packagingId: null,
  packagingQuantity: "",
  changed: false,
});

/**
 * A new, empty row of a kind: a catalog line to pick a variant for, a free-text
 * line, a section, a note or a shipping line (O53) — quantity 1, its price
 * typed or estimated.
 */
export const newLine = (kind: LineKind, catalog = false, name = ""): EditableLine => ({
  key: freshKey(),
  kind,
  catalog: kind === "product" && catalog,
  variantId: null,
  sku: null,
  name,
  description: "",
  quantity: kind === "product" || kind === "shipping" ? 1 : "",
  unit: kind === "product" ? "stk" : "",
  unitPrice: "",
  priceTouched: false,
  discountPercent: 0,
  vatCodeId: null,
  optional: false,
  packagings: [],
  packagingId: null,
  packagingQuantity: "",
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
  if (line.kind === "shipping") {
    // A shipping line (O53): quantity 1; the freight product's when it names
    // one, else free text with its own VAT code.
    const priced = line.unitPrice !== "" && (line.priceTouched || line.id === undefined);
    return {
      ...(line.id ? { id: line.id } : {}),
      kind: "shipping",
      variantId: line.variantId,
      ...(line.variantId === null ? { name: line.name, vatCodeId: line.vatCodeId } : {}),
      description: line.description,
      quantity: 1,
      unit: line.unit,
      ...(priced ? { unitPrice: line.unitPrice as number } : {}),
      ...(priced && line.carrierPriced ? { priceSource: "carrier" as const } : {}),
      discountPercent: line.discountPercent === "" ? 0 : line.discountPercent,
    };
  }
  if (line.kind !== "product")
    return { ...(line.id ? { id: line.id } : {}), kind: line.kind, description: line.description };
  const priced = line.unitPrice !== "" && (line.priceTouched || line.id === undefined);
  const packed = line.packagingId !== null;
  return {
    ...(line.id ? { id: line.id } : {}),
    kind: "product",
    variantId: line.variantId,
    ...(line.catalog ? {} : { name: line.name, vatCodeId: line.vatCodeId }),
    description: line.description,
    // A line entered in a packaging sends how many of it; the server stores
    // the quantity in the variant's unit (O15).
    ...(packed
      ? { packagingId: line.packagingId, packagingQuantity: line.packagingQuantity === "" ? 0 : line.packagingQuantity }
      : { quantity: line.quantity === "" ? 0 : line.quantity }),
    unit: line.unit,
    ...(priced ? { unitPrice: line.unitPrice as number } : {}),
    discountPercent: line.discountPercent === "" ? 0 : line.discountPercent,
    ...(quote ? { optional: line.optional } : {}),
    ...(!quote && line.policyTouched && line.invoicePolicy ? { invoicePolicy: line.invoicePolicy } : {}),
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
  // A template has no shipping lines (O53); its grid offers none.
  if (line.kind === "section" || line.kind === "note")
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
    else if (!template && line.packagingId !== null) {
      if (line.packagingQuantity === "" || line.packagingQuantity <= 0) problems.set(index, "lineQuantityMissing");
    } else if (!template && (line.quantity === "" || line.quantity <= 0)) problems.set(index, "lineQuantityMissing");
  });
  return problems;
};

/**
 * A saved line offered its variant's current packagings (O15) beside the one
 * it was saved in, while it keeps its saved variant; any other line as it is.
 */
export const withCurrentPackagings = (line: EditableLine, current: readonly VariantPackaging[]): EditableLine =>
  line.saved && line.variantId !== null && line.variantId === line.saved.variantId
    ? { ...line, packagings: savedPackagings(line.saved, current) }
    : line;

/**
 * A line entered in a packaging, as the grid and the PDF say it (O15):
 * "3 × Eske à 15" — the count, the packaging and its size — or null for a
 * line entered in its unit.
 */
export const packagingOf = (line: EditableLine): VariantPackaging | null =>
  line.packagingId === null ? null : (line.packagings.find((p) => p.id === line.packagingId) ?? null);

/**
 * The lines with the grid's quantities applied (O34): a quantity typed for a
 * variant updates its line entered in the unit when there is one, else adds a
 * catalog line for it; a cell left empty or at 0 changes nothing.
 */
export const applyGrid = (
  lines: readonly EditableLine[],
  picks: readonly { variantId: number; sku: string; name: string; unit: string; quantity: number }[],
): EditableLine[] => {
  const next = [...lines];
  for (const pick of picks) {
    if (!(pick.quantity > 0)) continue;
    const index = next.findIndex(
      (line) => line.kind === "product" && line.variantId === pick.variantId && line.packagingId === null,
    );
    if (index >= 0) next[index] = editLine(next[index], { quantity: pick.quantity });
    else
      next.push({
        ...newLine("product", true),
        variantId: pick.variantId,
        sku: pick.sku,
        name: pick.name,
        unit: pick.unit,
        quantity: pick.quantity,
      });
  }
  return next;
};
