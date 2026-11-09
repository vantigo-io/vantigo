import {
  COUNTS_ROUTE_PATH,
  IMPORT_ROUTE_PATH,
  INVENTORY_HOME_ROUTE_PATH,
  MOVEMENTS_ROUTE_PATH,
  TRANSFERS_ROUTE_PATH,
} from "./routes";

/** One sidebar destination of the app; the host owns its label and icon. */
export interface InventoryNavItem {
  key: "overview" | "transfers" | "movements" | "receipts" | "counts" | "import";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** The search the destination opens with: Receipts is the movements list filtered (design §13.3). */
  search?: Record<string, string>;
  /** Any one of these offers the entry (§5). */
  requiredPermissions: readonly string[];
  /** Every one of these is needed as well: a stock read needs both views on the server (§5). */
  requiredAllPermissions?: readonly string[];
}

const stockViews = ["inventory:locations-view", "inventory:stock-view"] as const;

export const inventoryNavItems: readonly InventoryNavItem[] = [
  {
    key: "overview",
    label: "navOverview",
    to: INVENTORY_HOME_ROUTE_PATH,
    requiredPermissions: ["inventory:locations-view", "inventory:stock-view"],
  },
  {
    // Seeing transfers needs both views alone (phase 2 design I63); writing them needs transfers-manage.
    key: "transfers",
    label: "navTransfers",
    to: TRANSFERS_ROUTE_PATH,
    requiredPermissions: ["inventory:stock-view"],
    requiredAllPermissions: stockViews,
  },
  {
    key: "movements",
    label: "navMovements",
    to: MOVEMENTS_ROUTE_PATH,
    requiredPermissions: ["inventory:stock-view"],
    requiredAllPermissions: stockViews,
  },
  {
    key: "receipts",
    label: "navReceipts",
    to: MOVEMENTS_ROUTE_PATH,
    search: { kind: "receipt" },
    requiredPermissions: ["inventory:stock-view"],
    requiredAllPermissions: stockViews,
  },
  {
    key: "counts",
    label: "navCounts",
    to: COUNTS_ROUTE_PATH,
    requiredPermissions: ["inventory:counts-manage"],
    requiredAllPermissions: ["inventory:locations-view", "inventory:stock-view"],
  },
  {
    key: "import",
    label: "navImport",
    to: IMPORT_ROUTE_PATH,
    requiredPermissions: ["inventory:stock-adjust"],
    requiredAllPermissions: ["inventory:locations-view", "inventory:stock-view"],
  },
];

/** The entries the caller's permissions offer. */
export const visibleInventoryNavItems = (permissions: readonly string[] | undefined) =>
  inventoryNavItems.filter(
    (item) =>
      permissions?.includes("*") === true ||
      (item.requiredPermissions.some((p) => permissions?.includes(p)) &&
        (item.requiredAllPermissions ?? []).every((p) => permissions?.includes(p))),
  );
