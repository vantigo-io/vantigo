import {
  ACCOUNTING_HOME_ROUTE_PATH,
  ACCOUNTS_ROUTE_PATH,
  JOURNAL_ROUTE_PATH,
  OPEN_ITEMS_ROUTE_PATH,
  REPORTS_ROUTE_PATH,
  SETTINGS_ROUTE_PATH,
  TAX_CODES_ROUTE_PATH,
  VOUCHERS_ROUTE_PATH,
} from "./routes";

/** One sidebar destination of the app; the host owns its label and icon. */
export interface AccountingNavItem {
  key: "overview" | "journal" | "vouchers" | "openItems" | "reports" | "accounts" | "taxCodes" | "settings";
  /** The key of the destination's label in the package catalog. */
  label: string;
  to: string;
  /** Any one of these offers the entry (A1). */
  requiredPermissions: readonly string[];
}

const read = ["accounting:read"] as const;

/**
 * The sidebar: the overview, a new journal entry for those who post, the
 * vouchers, the open items, the reports, then the chart, the tax codes and
 * the books' settings. Set up the books and the opening balances have no
 * entry: the overview leads to them.
 */
export const accountingNavItems: readonly AccountingNavItem[] = [
  { key: "overview", label: "navOverview", to: ACCOUNTING_HOME_ROUTE_PATH, requiredPermissions: read },
  { key: "journal", label: "navJournal", to: JOURNAL_ROUTE_PATH, requiredPermissions: ["accounting:post"] },
  { key: "vouchers", label: "navVouchers", to: VOUCHERS_ROUTE_PATH, requiredPermissions: read },
  { key: "openItems", label: "navOpenItems", to: OPEN_ITEMS_ROUTE_PATH, requiredPermissions: read },
  { key: "reports", label: "navReports", to: REPORTS_ROUTE_PATH, requiredPermissions: read },
  { key: "accounts", label: "navAccounts", to: ACCOUNTS_ROUTE_PATH, requiredPermissions: read },
  { key: "taxCodes", label: "navTaxCodes", to: TAX_CODES_ROUTE_PATH, requiredPermissions: read },
  { key: "settings", label: "navSettings", to: SETTINGS_ROUTE_PATH, requiredPermissions: read },
];

/** The entries the caller's permissions offer. */
export const visibleAccountingNavItems = (permissions: readonly string[] | undefined) =>
  accountingNavItems.filter(
    (item) => permissions?.includes("*") === true || item.requiredPermissions.some((p) => permissions?.includes(p)),
  );
