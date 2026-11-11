import { BOMS_ROUTE_PATH, MANUFACTURING_HOME_ROUTE_PATH, WORK_CENTERS_ROUTE_PATH } from "./routes";

/** One sidebar destination of the app (manufacturing design M7); the host owns its label and icon. */
export interface ManufacturingNavItem {
  key: "overview" | "boms" | "workCenters";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry (§8). */
  requiredPermissions: readonly string[];
}

/** Every Manufacturing read asks for the app's own key (§8). */
const access = ["manufacturing:access"] as const;

/**
 * The sidebar in M7's order. Delivery A has three of M7's six destinations;
 * the other three take their places between Overview and Bills of materials
 * as they come: Manufacturing orders (`navOrders`, B), Shop floor
 * (`navShopFloor`, C) and Production schedule (`navSchedule`, D). Their labels
 * are in the catalog already.
 */
export const manufacturingNavItems: readonly ManufacturingNavItem[] = [
  { key: "overview", label: "navOverview", to: MANUFACTURING_HOME_ROUTE_PATH, requiredPermissions: access },
  // B: { key: "orders", label: "navOrders", to: "/manufacturing/orders", … }
  // C: { key: "shopFloor", label: "navShopFloor", to: "/manufacturing/shop-floor", … }
  // D: { key: "schedule", label: "navSchedule", to: "/manufacturing/schedule", … }
  { key: "boms", label: "navBoms", to: BOMS_ROUTE_PATH, requiredPermissions: access },
  { key: "workCenters", label: "navWorkCenters", to: WORK_CENTERS_ROUTE_PATH, requiredPermissions: access },
];

/** The entries the caller's permissions offer. */
export const visibleManufacturingNavItems = (permissions: readonly string[] | undefined) =>
  manufacturingNavItems.filter(
    (item) => permissions?.includes("*") === true || item.requiredPermissions.some((p) => permissions?.includes(p)),
  );
