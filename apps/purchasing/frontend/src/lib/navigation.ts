import { PURCHASE_ORDERS_ROUTE_PATH, SETTINGS_ROUTE_PATH, SUPPLIERS_ROUTE_PATH } from "./routes";

/** One sidebar destination of the app (purchasing design P23); the host owns its label and icon. */
export interface PurchasingNavItem {
  key: "orders" | "suppliers" | "settings";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry (§8). */
  requiredPermissions: readonly string[];
  /** Every one of these is needed as well: every Purchasing call asks for purchasing:access. */
  requiredAllPermissions?: readonly string[];
}

const access = ["purchasing:access"] as const;

export const purchasingNavItems: readonly PurchasingNavItem[] = [
  { key: "orders", label: "navOrders", to: PURCHASE_ORDERS_ROUTE_PATH, requiredPermissions: access },
  { key: "suppliers", label: "navSuppliers", to: SUPPLIERS_ROUTE_PATH, requiredPermissions: access },
  {
    key: "settings",
    label: "navSettings",
    to: SETTINGS_ROUTE_PATH,
    requiredPermissions: ["purchasing:settings"],
    requiredAllPermissions: access,
  },
];

/** The entries the caller's permissions offer. */
export const visiblePurchasingNavItems = (permissions: readonly string[] | undefined) =>
  purchasingNavItems.filter(
    (item) =>
      permissions?.includes("*") === true ||
      (item.requiredPermissions.some((p) => permissions?.includes(p)) &&
        (item.requiredAllPermissions ?? []).every((p) => permissions?.includes(p))),
  );
