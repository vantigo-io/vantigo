/** The app's paths, which the host mounts and the package's own test route tree mirrors. */
export const ACCOUNTING_HOME_ROUTE_PATH = "/accounting";
export const SETUP_ROUTE_PATH = "/accounting/setup";
export const VOUCHERS_ROUTE_PATH = "/accounting/vouchers";
export const VOUCHER_ROUTE_PATH = "/accounting/vouchers/$voucherId";
export const JOURNAL_ROUTE_PATH = "/accounting/journal";
export const ACCOUNTS_ROUTE_PATH = "/accounting/accounts";
export const TAX_CODES_ROUTE_PATH = "/accounting/tax-codes";
export const OPEN_ITEMS_ROUTE_PATH = "/accounting/open-items";
export const OPENING_ROUTE_PATH = "/accounting/opening";
export const REPORTS_ROUTE_PATH = "/accounting/reports";
export const SETTINGS_ROUTE_PATH = "/accounting/settings";
export const POSTING_RULES_ROUTE_PATH = "/accounting/posting-rules";
export const NEEDS_ATTENTION_ROUTE_PATH = "/accounting/needs-attention";
export const SUPPLIERS_ROUTE_PATH = "/accounting/suppliers";
export const SUPPLIER_ROUTE_PATH = "/accounting/suppliers/$supplierId";
export const SUPPLIER_INVOICES_ROUTE_PATH = "/accounting/supplier-invoices";
export const SUPPLIER_INVOICE_ROUTE_PATH = "/accounting/supplier-invoices/$supplierInvoiceId";
export const NEW_SUPPLIER_INVOICE_ROUTE_PATH = "/accounting/supplier-invoices/new";

/** A voucher's page. */
export const voucherPath = (id: number) => `/accounting/vouchers/${id}`;

/** An invoice's page in the Invoices app, which a voucher and a report row link to (A13). */
export const invoicePath = (id: number) => `/invoices/${id}`;

/** A supplier's page. */
export const supplierPath = (id: number) => `/accounting/suppliers/${id}`;

/** A supplier invoice's page. */
export const supplierInvoicePath = (id: number) => `/accounting/supplier-invoices/${id}`;
