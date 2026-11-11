import type { ManufacturingOrder, ManufacturingOrderComponent, ManufacturingOrderLink } from "../api/orders";
import { addDecimal, compareDecimal, subtractDecimal } from "./decimal";

/** A component line a sub-assembly order can be made for, and the quantity it would be made for. */
export interface SubAssemblyLine {
  line: ManufacturingOrderComponent;
  /** What the line misses less what the open sub-assembly orders of its variant still make. */
  missing: string;
}

/** The order's sub-assembly orders not done or cancelled, each with what it still makes. */
export const openChildren = (order: ManufacturingOrder): { child: ManufacturingOrderLink; remaining: string }[] =>
  order.children
    .filter((child) => child.status === "draft" || child.status === "confirmed" || child.status === "in_progress")
    .map((child) => ({ child, remaining: subtractDecimal(child.quantity, child.produced) }));

/**
 * The component lines a sub-assembly order can be made for (M47): those
 * whose variant has a default active BoM and that miss more than the open
 * sub-assembly orders of the variant still make — the server makes a new one
 * for that rest only.
 */
export const subAssemblyLines = (order: ManufacturingOrder): SubAssemblyLine[] => {
  const making = new Map<number, string>();
  for (const { child, remaining } of openChildren(order)) {
    const sum = making.get(child.variantId) ?? "0";
    making.set(child.variantId, addDecimal(sum, remaining));
  }
  return order.components
    .filter((line) => line.id != null && line.defaultBomId != null)
    .map((line) => ({ line, missing: subtractDecimal(line.missing, making.get(line.variant.id) ?? "0") }))
    .filter(({ missing }) => compareDecimal(missing, "0") > 0);
};
