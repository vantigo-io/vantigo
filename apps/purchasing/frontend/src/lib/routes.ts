/** The app's paths (purchasing design P23), which the host mounts and the package's links name. */
export const PURCHASING_HOME_ROUTE_PATH = "/purchasing";
export const PURCHASE_ORDERS_ROUTE_PATH = "/purchasing/orders";
export const NEW_PURCHASE_ORDER_ROUTE_PATH = "/purchasing/orders/new";
export const PURCHASE_ORDER_ROUTE_PATH = "/purchasing/orders/$orderId";
/** The orders to receive (P30, A47): purchasing:receive's own list, beside the orders. */
export const RECEIVING_ROUTE_PATH = "/purchasing/receive";
/** The receive screen of a sent order (P24), under the receiving list so purchasing:receive alone reaches it. */
export const RECEIVE_PURCHASE_ORDER_ROUTE_PATH = "/purchasing/receive/$orderId";
export const SUPPLIERS_ROUTE_PATH = "/purchasing/suppliers";
export const SUPPLIER_ROUTE_PATH = "/purchasing/suppliers/$supplierId";
export const SETTINGS_ROUTE_PATH = "/purchasing/settings";
export const PROPOSALS_ROUTE_PATH = "/purchasing/proposals";
/** Received, not invoiced, as of a day (P41). */
export const RECEIVED_NOT_INVOICED_ROUTE_PATH = "/purchasing/received-not-invoiced";

export const purchaseOrderLinkOptions = (orderId: number) => ({
  to: PURCHASE_ORDER_ROUTE_PATH,
  params: { orderId: String(orderId) },
});

export const receivePurchaseOrderLinkOptions = (orderId: number) => ({
  to: RECEIVE_PURCHASE_ORDER_ROUTE_PATH,
  params: { orderId: String(orderId) },
});

export const supplierLinkOptions = (supplierId: number) => ({
  to: SUPPLIER_ROUTE_PATH,
  params: { supplierId: String(supplierId) },
});

/** A new order's editor, with the supplier filled in when one is given (from a supplier's page). */
export const newPurchaseOrderLinkOptions = (supplierId?: number) => ({
  to: NEW_PURCHASE_ORDER_ROUTE_PATH,
  search: supplierId === undefined ? {} : { supplierId },
});

/** The new order's search: the supplier it opens with, when a positive whole number. */
export interface NewPurchaseOrderSearch {
  supplierId?: number;
}

export const validateNewPurchaseOrderSearch = (search: Record<string, unknown>): NewPurchaseOrderSearch => {
  const id = Number(search.supplierId);
  return Number.isSafeInteger(id) && id > 0 ? { supplierId: id } : {};
};

/** Accounting's supplier page (owned by the Accounting app), linked for a holder of accounting:read. */
const ACCOUNTING_SUPPLIER_ROUTE_PATH = "/accounting/suppliers/$supplierId";

export const accountingSupplierLinkOptions = (supplierId: number) => ({
  to: ACCOUNTING_SUPPLIER_ROUTE_PATH,
  params: { supplierId: String(supplierId) },
});

/** Products' product page (owned by the Products app), where a supplier item's product links. */
const PRODUCT_ROUTE_PATH = "/products/$productId";

export const productLinkOptions = (productId: number) => ({
  to: PRODUCT_ROUTE_PATH,
  params: { productId: String(productId) },
});

/** Inventory's movement page (owned by the Inventory app), where a receipt and its undo link. */
const INVENTORY_MOVEMENT_ROUTE_PATH = "/inventory/movements/$movementId";

export const inventoryMovementLinkOptions = (movementId: number) => ({
  to: INVENTORY_MOVEMENT_ROUTE_PATH,
  params: { movementId: String(movementId) },
});

/** The proposals' search (P35): one variant, one site, each when a positive whole number. */
export interface ProposalsSearch {
  variantId?: number;
  siteId?: number;
}

export const validateProposalsSearch = (search: Record<string, unknown>): ProposalsSearch => {
  const out: ProposalsSearch = {};
  for (const key of ["variantId", "siteId"] as const) {
    const id = Number(search[key]);
    if (Number.isSafeInteger(id) && id > 0) out[key] = id;
  }
  return out;
};

/** The proposals, filtered to a variant (and its site) when a link in names one. */
export const proposalsLinkOptions = (search: ProposalsSearch = {}) => ({
  to: PROPOSALS_ROUTE_PATH,
  search,
});
