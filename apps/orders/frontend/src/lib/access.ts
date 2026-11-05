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
  /** `orders:deliveries-manage`: create, pick, post and cancel deliveries (O47). */
  canManageDeliveries: boolean;
  /** `orders:subscriptions-manage`: create, activate, change, pause, resume, end and cancel subscriptions, and accept a quote as one (O42). */
  canManageSubscriptions: boolean;
  /** `orders:settings`: the settings, the templates and the warnings, and a warning's override. */
  canManageSettings: boolean;
  /** `orders:analysis-view`: the analysis of quotes and orders (O35). */
  canViewAnalysis: boolean;
  /** `customers:view`: the customer picker and a customer's addresses. */
  canViewCustomers: boolean;
  /** `customers:contacts-view` and `customers:associations-view`: a customer's contacts. */
  canViewContacts: boolean;
  /** The invoices module mounted and `invoices:access`: the VAT codes a free-text line picks among. */
  canReadVatCodes: boolean;
  /** The projects module mounted and `projects:access`: the project picker. */
  canViewProjects: boolean;
  /**
   * The invoices module mounted, `invoices:access` and `invoices:create`: an
   * order's *Invoice*, which opens Invoices' *From orders* wizard (O39).
   */
  canCreateInvoices: boolean;
  /** The invoices module mounted and `invoices:access`: an order's invoices link to Invoices (O41). */
  canViewInvoices: boolean;
  /** The signed-in user, who may make themself a document's salesperson. */
  userId?: string;
  userDisplayName?: string;
}

/** Nothing allowed: what a test or a host that has not answered yet hands in. */
export const noOrdersAccess: OrdersAccess = {
  canManageQuotes: false,
  canManageOrders: false,
  canConfirmOrders: false,
  canManageDeliveries: false,
  canManageSubscriptions: false,
  canManageSettings: false,
  canViewAnalysis: false,
  canViewCustomers: false,
  canViewContacts: false,
  canReadVatCodes: false,
  canViewProjects: false,
  canCreateInvoices: false,
  canViewInvoices: false,
};
