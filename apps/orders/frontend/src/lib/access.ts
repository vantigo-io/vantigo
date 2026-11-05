/**
 * What the caller may do in Orders and in the modules its screens read from,
 * as the host judges it from the session's permissions (§8) and the modules
 * mounted. The pages never read the session themselves: every flag is the
 * host's answer, handed in (apps/host/frontend/src/lib/orders-access.ts).
 */
export interface OrdersAccess {
  /** `orders:quotes-manage`: create, edit, send, revise, accept, decline and cancel quotes. */
  canManageQuotes: boolean;
  /** `orders:sales-manage`: create and edit orders, hold, cancel, mark delivered. */
  canManageOrders: boolean;
  /** `orders:sales-confirm`: confirm, lock and unlock, and accept a quote into a confirmed order. */
  canConfirmOrders: boolean;
  /** `orders:settings`: the settings and the templates. */
  canManageSettings: boolean;
  /** `customers:view`: the customer picker and a customer's addresses. */
  canViewCustomers: boolean;
  /** `customers:contacts-view` and `customers:associations-view`: a customer's contacts. */
  canViewContacts: boolean;
  /** The invoices module mounted and `invoices:access`: the VAT codes a free-text line picks among. */
  canReadVatCodes: boolean;
  /** The projects module mounted and `projects:access`: the project picker. */
  canViewProjects: boolean;
  /** The signed-in user, who may make themself a document's salesperson. */
  userId?: string;
  userDisplayName?: string;
}

/** Nothing allowed: what a test or a host that has not answered yet hands in. */
export const noOrdersAccess: OrdersAccess = {
  canManageQuotes: false,
  canManageOrders: false,
  canConfirmOrders: false,
  canManageSettings: false,
  canViewCustomers: false,
  canViewContacts: false,
  canReadVatCodes: false,
  canViewProjects: false,
};
