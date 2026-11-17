/**
 * What the caller may do in Purchasing (purchasing design §8), as the host
 * judges it from the session's permissions. The pages never read the session
 * themselves: every flag is the host's answer, handed in
 * (apps/host/frontend/src/lib/purchasing-access.ts). Whether the caller may
 * write a supplier in the register is the meta's (`canManageSuppliers`, A12).
 */
export interface PurchasingAccess {
  /** `purchasing:orders-manage`: write drafts, change sent orders, record a confirmation, close, reopen, close a line, copy. */
  canManageOrders: boolean;
  /** `purchasing:orders-send`: send, mark as sent, send again, cancel. */
  canSendOrders: boolean;
  /** `purchasing:supplier-items-manage`: write and import supplier items. */
  canManageSupplierItems: boolean;
  /** `purchasing:settings`: the settings. */
  canManageSettings: boolean;
  /** `purchasing:receive` — alone, without purchasing:access (P30, A47): receive goods, undo a receipt, mark a text or service line delivered. */
  canReceive: boolean;
  /** `purchasing:access`: read orders, their prices and receipts. A receiver without it lands on the receiving list. */
  canRead?: boolean;
  /** Inventory is enabled and the caller holds inventory:stock-view: a receipt links its movement. */
  canOpenInventoryMovements?: boolean;
  /** The signed-in user, our reference on a new order. */
  userId?: string;
}

/** Nothing allowed: what a test or a host that has not answered yet hands in. */
export const noPurchasingAccess: PurchasingAccess = {
  canManageOrders: false,
  canSendOrders: false,
  canManageSupplierItems: false,
  canManageSettings: false,
  canReceive: false,
};
