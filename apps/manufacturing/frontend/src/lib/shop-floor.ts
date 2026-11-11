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
