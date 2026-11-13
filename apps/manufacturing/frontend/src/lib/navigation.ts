import {
  BOMS_ROUTE_PATH,
  DEVIATIONS_ROUTE_PATH,
  MANUFACTURING_HOME_ROUTE_PATH,
  ORDERS_ROUTE_PATH,
  SCHEDULE_ROUTE_PATH,
  SHOP_FLOOR_ROUTE_PATH,
  SUGGESTIONS_ROUTE_PATH,
  UNBUILDS_ROUTE_PATH,
  WORK_CENTERS_ROUTE_PATH,
} from "./routes";

/** One sidebar destination of the app (manufacturing design M7); the host owns its label and icon. */
export interface ManufacturingNavItem {
  key:
    | "overview"
    | "orders"
    | "suggestions"
    | "unbuilds"
    | "shopFloor"
    | "schedule"
    | "deviations"
    | "boms"
    | "workCenters";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry (§8). */
  requiredPermissions: readonly string[];
}

/** Every Manufacturing read asks for the app's own key (§8). */
const access = ["manufacturing:access"] as const;

/**
 * The sidebar in M7's order, with the suggestions after the orders and the
 * deviations after the schedule. The shop floor and the
 * schedule are read with the app's key; what may be done there is the
 * meta's (M33, M36). The reports have no entry of their own: they live under
 * the overview, whose entry the longest-prefix highlight keeps lit on
 * `/manufacturing/reports`.
 */
export const manufacturingNavItems: readonly ManufacturingNavItem[] = [
  { key: "overview", label: "navOverview", to: MANUFACTURING_HOME_ROUTE_PATH, requiredPermissions: access },
  { key: "orders", label: "navOrders", to: ORDERS_ROUTE_PATH, requiredPermissions: access },
  // The replenishment suggestions (manufacturing phase 2 M111); acting on them is orders-manage's.
  { key: "suggestions", label: "navSuggestions", to: SUGGESTIONS_ROUTE_PATH, requiredPermissions: access },
  // The unbuilds (manufacturing phase 2 M123), read with the app's key; one is made from a done order's page.
  { key: "unbuilds", label: "navUnbuilds", to: UNBUILDS_ROUTE_PATH, requiredPermissions: access },
  { key: "shopFloor", label: "navShopFloor", to: SHOP_FLOOR_ROUTE_PATH, requiredPermissions: access },
  { key: "schedule", label: "navSchedule", to: SCHEDULE_ROUTE_PATH, requiredPermissions: access },
  // The deviations (manufacturing phase 2 M99), read with the app's key; closing one is quality-manage's.
  { key: "deviations", label: "navDeviations", to: DEVIATIONS_ROUTE_PATH, requiredPermissions: access },
  { key: "boms", label: "navBoms", to: BOMS_ROUTE_PATH, requiredPermissions: access },
  { key: "workCenters", label: "navWorkCenters", to: WORK_CENTERS_ROUTE_PATH, requiredPermissions: access },
];

/** The entries the caller's permissions offer. */
export const visibleManufacturingNavItems = (permissions: readonly string[] | undefined) =>
  manufacturingNavItems.filter(
    (item) => permissions?.includes("*") === true || item.requiredPermissions.some((p) => permissions?.includes(p)),
  );
