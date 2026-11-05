import { QUOTES_ROUTE_PATH, SALES_ORDERS_ROUTE_PATH, SETTINGS_ROUTE_PATH, TEMPLATES_ROUTE_PATH } from "./routes";

/** One sidebar destination of the app (orders design O11); the host owns its label and icon. */
export interface OrdersNavItem {
  key: "quotes" | "orders" | "templates" | "settings";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry (§8). */
  requiredPermissions: readonly string[];
  /** Every one of these is needed as well: every Orders call asks for orders:access. */
  requiredAllPermissions?: readonly string[];
}

const access = ["orders:access"] as const;

export const ordersNavItems: readonly OrdersNavItem[] = [
  { key: "quotes", label: "navQuotes", to: QUOTES_ROUTE_PATH, requiredPermissions: access },
  { key: "orders", label: "navOrders", to: SALES_ORDERS_ROUTE_PATH, requiredPermissions: access },
  {
    key: "templates",
    label: "navTemplates",
    to: TEMPLATES_ROUTE_PATH,
    requiredPermissions: ["orders:settings"],
    requiredAllPermissions: access,
  },
  {
    key: "settings",
    label: "navSettings",
    to: SETTINGS_ROUTE_PATH,
    requiredPermissions: ["orders:settings"],
    requiredAllPermissions: access,
  },
];

/** The entries the caller's permissions offer. */
export const visibleOrdersNavItems = (permissions: readonly string[] | undefined) =>
  ordersNavItems.filter(
    (item) =>
      permissions?.includes("*") === true ||
      (item.requiredPermissions.some((p) => permissions?.includes(p)) &&
        (item.requiredAllPermissions ?? []).every((p) => permissions?.includes(p))),
  );
