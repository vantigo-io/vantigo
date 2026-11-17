import type {
  PurchaseLineInput,
  PurchaseLineWarning,
  PurchaseOrderLine,
  PurchasingPackaging,
  PurchasingVariantOption,
} from "../api/purchasing";
import { addDays } from "./format";

/** A line as the editor holds it: text fields as typed, the saved line's id when it has one (P16). */
export interface EditorLine {
  key: string;
  id?: number;
  kind: "variant" | "text";
  variantId?: number;
  sku?: string;
  name: string;
  unit: string;
  /** The variant's packagings, for a new line's packaging choice. */
  packagings: PurchasingPackaging[];
  packagingId: number | null;
  packagingName?: string | null;
  orderQuantity: string;
  unitPrice: string;
  discountPercent: string;
  expectedOn: string | null;
  supplierItemNumber: string;
  /** The supplier item's minimum and multiple, in purchase packagings, which the warnings weigh. */
  minimum?: string | null;
  multiple?: string | null;
  /** A saved line's figures, the server's. */
  received?: string;
  lineNet?: string;
  closed?: boolean;
  warnings: PurchaseLineWarning[];
}

let counter = 0;
const nextKey = () => {
  counter += 1;
  return `new-${counter}`;
};

/** A saved line as the editor starts from it. */
export const fromSavedLine = (line: PurchaseOrderLine): EditorLine => ({
  key: `line-${line.id}`,
  id: line.id,
  kind: line.kind,
  variantId: line.variantId ?? undefined,
  sku: line.sku ?? undefined,
  name: line.kind === "text" ? line.description : line.name,
  unit: line.unit,
  packagings:
    line.packagingId !== null && line.packagingId !== undefined
      ? [{ id: line.packagingId, name: line.packagingName ?? "", quantity: line.packagingQuantity ?? "1" }]
      : [],
  packagingId: line.packagingId ?? null,
  packagingName: line.packagingName,
  orderQuantity: line.orderQuantity,
  unitPrice: line.unitPrice,
  discountPercent: line.discountPercent,
  expectedOn: line.expectedOn ?? null,
  supplierItemNumber: line.supplierItemNumber ?? "",
  received: line.received,
  lineNet: line.lineNet,
  closed: line.closedAt !== null && line.closedAt !== undefined,
  warnings: line.warnings,
});

/**
 * A new line for a picked variant, filled from the supplier's item when the
 * supplier sells it (P16): the supplier's item number, the price, the
 * packaging (when still one of the variant's) and the expected day — the
 * order date plus the lead time.
 */
export const fromVariant = (variant: PurchasingVariantOption, orderDate: string): EditorLine => {
  const item = variant.supplierItem ?? null;
  const packagingId =
    item?.packagingId !== null && item?.packagingId !== undefined && !item.packagingMissing
      ? variant.packagings.some((p) => p.id === item.packagingId)
        ? item.packagingId
        : null
      : null;
  return {
    key: nextKey(),
    kind: "variant",
    variantId: variant.variantId,
    sku: variant.sku,
    name: variant.name,
    unit: variant.unit,
    packagings: variant.packagings,
    packagingId,
    orderQuantity: item?.minimumQuantity ?? "1",
    unitPrice: item?.price ?? "",
    discountPercent: "",
    expectedOn:
      item?.leadTimeDays !== null && item?.leadTimeDays !== undefined ? addDays(orderDate, item.leadTimeDays) : null,
    supplierItemNumber: item?.supplierItemNumber ?? "",
    minimum: item?.minimumQuantity ?? null,
    multiple: item?.multiple ?? null,
    warnings: [],
  };
};

/** A new text line: freight, a service, something outside the catalog. */
export const newTextLine = (): EditorLine => ({
  key: nextKey(),
  kind: "text",
  name: "",
  unit: "",
  packagings: [],
  packagingId: null,
  orderQuantity: "1",
  unitPrice: "",
  discountPercent: "",
  expectedOn: null,
  supplierItemNumber: "",
  warnings: [],
});

/** A decimal text as thousandths, or undefined when it is not a number. */
const thousandths = (value: string | null | undefined): number | undefined => {
  if (value === null || value === undefined || value.trim() === "") return undefined;
  const n = Number(value.replace(",", "."));
  return Number.isFinite(n) ? Math.round(n * 1000) : undefined;
};

/**
 * The warnings a line's quantity draws against the supplier item (P16): under
 * the minimum, or off the multiple. A warning, never a refusal. A saved line
 * keeps the server's until its quantity is changed.
 */
export const lineWarnings = (line: EditorLine): PurchaseLineWarning[] => {
  if (line.minimum === undefined && line.multiple === undefined) return line.warnings;
  const qty = thousandths(line.orderQuantity);
  if (qty === undefined) return [];
  const out: PurchaseLineWarning[] = [];
  const min = thousandths(line.minimum);
  const mult = thousandths(line.multiple);
  if (min !== undefined && qty < min) out.push("below_minimum");
  if (mult !== undefined && mult > 0 && qty % mult !== 0) out.push("off_multiple");
  return out;
};

/** A decimal as typed, with a comma read as the point. */
export const decimal = (value: string) => value.trim().replace(",", ".");

/**
 * The line as the contract takes it. Every saved line is named by its id; a
 * field left out keeps its value (a saved line's packaging) or comes from the
 * supplier's item (a new line's number and price).
 */
export const lineInput = (line: EditorLine): PurchaseLineInput => {
  const saved = line.id !== undefined;
  const base: PurchaseLineInput = {
    kind: line.kind,
    orderQuantity: decimal(line.orderQuantity),
    expectedOn: line.expectedOn,
    ...(saved ? { id: line.id } : {}),
  };
  if (line.discountPercent.trim() !== "") base.discountPercent = decimal(line.discountPercent);
  else if (saved) base.discountPercent = "0";
  if (line.kind === "text") {
    return {
      ...base,
      description: line.name.trim(),
      unit: line.unit.trim() || null,
      unitPrice: line.unitPrice.trim() === "" ? "0" : decimal(line.unitPrice),
    };
  }
  const out: PurchaseLineInput = { ...base, variantId: line.variantId ?? null };
  if (line.unitPrice.trim() !== "") out.unitPrice = decimal(line.unitPrice);
  if (line.supplierItemNumber.trim() !== "") out.supplierItemNumber = line.supplierItemNumber.trim();
  else if (saved) out.supplierItemNumber = null;
  if (!saved) out.packagingId = line.packagingId;
  return out;
};
