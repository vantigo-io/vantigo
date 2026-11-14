import type { ManufacturingWorkOrder } from "../api/shop-floor";
import { compareDecimal, subtractDecimal } from "./decimal";
import type { useManufacturingFormat } from "./format";
import { MANUFACTURING_HOME_ROUTE_PATH, SHOP_FLOOR_ROUTE_PATH } from "./routes";

type TFunction = ReturnType<typeof useManufacturingFormat>["t"];

/** The shop floor's breadcrumbs: Manufacturing › Shop floor, then the screen's own when it has one. */
export const shopFloorBreadcrumbs = (t: TFunction, label?: string) => [
  { label: t("manufacturing"), to: MANUFACTURING_HOME_ROUTE_PATH },
  label ? { label: t("navShopFloor"), to: SHOP_FLOOR_ROUTE_PATH } : { label: t("navShopFloor") },
  ...(label ? [{ label }] : []),
];

/** A shop-floor card is a large touch target (M31): at least this tall. */
export const TOUCH_CARD_MIN_HEIGHT = 96;

/**
 * How far one work order has come: the good units it has registered — Register
 * quantity's for an operation that passes them on, the order's production for
 * its last — and what is left of the order's quantity for it, never below 0.
 */
export const operationProgress = (
  workOrder: Pick<ManufacturingWorkOrder, "last" | "quantityDone" | "order">,
): { done: string; remaining: string } => {
  const passed = workOrder.quantityDone ?? "0";
  const done =
    workOrder.last && compareDecimal(workOrder.order.produced, passed) > 0 ? workOrder.order.produced : passed;
  return { done, remaining: subtractDecimal(workOrder.order.quantity, done) };
};
