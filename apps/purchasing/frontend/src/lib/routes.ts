/** The app's paths (purchasing design P23), which the host mounts and the package's links name. */
export const PURCHASING_HOME_ROUTE_PATH = "/purchasing";
export const PURCHASE_ORDERS_ROUTE_PATH = "/purchasing/orders";
export const NEW_PURCHASE_ORDER_ROUTE_PATH = "/purchasing/orders/new";
export const PURCHASE_ORDER_ROUTE_PATH = "/purchasing/orders/$orderId";
/** The receive screen of a sent order (P24). */
export const RECEIVE_PURCHASE_ORDER_ROUTE_PATH = "/purchasing/orders/$orderId/receive";
export const SUPPLIERS_ROUTE_PATH = "/purchasing/suppliers";
export const SUPPLIER_ROUTE_PATH = "/purchasing/suppliers/$supplierId";
export const SETTINGS_ROUTE_PATH = "/purchasing/settings";

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
