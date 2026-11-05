/** The app's paths (orders design O11), which the host mounts and the package's links name. */
export const ORDERS_HOME_ROUTE_PATH = "/orders";
export const QUOTES_ROUTE_PATH = "/orders/quotes";
export const NEW_QUOTE_ROUTE_PATH = "/orders/quotes/new";
export const QUOTE_ROUTE_PATH = "/orders/quotes/$quoteId";
export const SALES_ORDERS_ROUTE_PATH = "/orders/orders";
export const NEW_SALES_ORDER_ROUTE_PATH = "/orders/orders/new";
export const SALES_ORDER_ROUTE_PATH = "/orders/orders/$orderId";
export const SUBSCRIPTIONS_ROUTE_PATH = "/orders/subscriptions";
export const NEW_SUBSCRIPTION_ROUTE_PATH = "/orders/subscriptions/new";
export const SUBSCRIPTION_ROUTE_PATH = "/orders/subscriptions/$subscriptionId";
export const TEMPLATES_ROUTE_PATH = "/orders/templates";
export const SETTINGS_ROUTE_PATH = "/orders/settings";
export const ANALYSIS_ROUTE_PATH = "/orders/analysis";

export const quoteLinkOptions = (quoteId: number) => ({ to: QUOTE_ROUTE_PATH, params: { quoteId: String(quoteId) } });
export const salesOrderLinkOptions = (orderId: number) => ({
  to: SALES_ORDER_ROUTE_PATH,
  params: { orderId: String(orderId) },
});

export const subscriptionLinkOptions = (subscriptionId: number) => ({
  to: SUBSCRIPTION_ROUTE_PATH,
  params: { subscriptionId: String(subscriptionId) },
});

/** The search a new quote's or order's editor opens with: the customer, when it was opened from the customer page (O31a). */
const customerSearch = (customerId?: number): Record<string, number> =>
  customerId === undefined ? {} : { customerId };

/** A new quote's or order's editor, with the customer filled in when one is given. */
export const newQuoteLinkOptions = (customerId?: number) => ({
  to: NEW_QUOTE_ROUTE_PATH,
  search: customerSearch(customerId),
});
export const newSalesOrderLinkOptions = (customerId?: number) => ({
  to: NEW_SALES_ORDER_ROUTE_PATH,
  search: customerSearch(customerId),
});
export const newSubscriptionLinkOptions = (customerId?: number) => ({
  to: NEW_SUBSCRIPTION_ROUTE_PATH,
  search: customerSearch(customerId),
});

/**
 * Invoices' routes an order links to (orders design O39, O41), owned by the
 * Invoices app: its *From orders* wizard with an order chosen, and one
 * invoice.
 */
const INVOICES_FROM_ORDERS_ROUTE_PATH = "/invoices/new/from-orders";
const INVOICE_ROUTE_PATH = "/invoices/$invoiceId";

export const invoiceFromOrderLinkOptions = (orderId: number) => ({
  to: INVOICES_FROM_ORDERS_ROUTE_PATH,
  search: { orderId },
});
export const invoiceLinkOptions = (invoiceId: number) => ({
  to: INVOICE_ROUTE_PATH,
  params: { invoiceId: String(invoiceId) },
});
