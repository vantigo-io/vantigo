import "./i18n";

export * from "./api/request";
export { assistTargets } from "./assist-targets";
export { CustomerOrdersPanel, type CustomerOrdersPanelProps } from "./components/customer-orders-panel";
export { ordersCatalog } from "./i18n";
export { noOrdersAccess, type OrdersAccess } from "./lib/access";
export { type OrdersNavItem, ordersNavItems, visibleOrdersNavItems } from "./lib/navigation";
export * from "./lib/routes";
export { OrderEditorPage } from "./pages/order-editor";
export { OrdersPage } from "./pages/orders";
export { QuoteEditorPage } from "./pages/quote-editor";
export { QuotesPage } from "./pages/quotes";
export { SettingsPage } from "./pages/settings";
export { TemplatesPage } from "./pages/templates";
