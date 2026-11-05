import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import "../i18n";
import { CustomerOrdersPanel } from "../components/customer-orders-panel";
import { noOrdersAccess, type OrdersAccess } from "../lib/access";
import {
  NEW_QUOTE_ROUTE_PATH,
  NEW_SALES_ORDER_ROUTE_PATH,
  QUOTE_ROUTE_PATH,
  QUOTES_ROUTE_PATH,
  SALES_ORDER_ROUTE_PATH,
  SALES_ORDERS_ROUTE_PATH,
  SETTINGS_ROUTE_PATH,
  TEMPLATES_ROUTE_PATH,
} from "../lib/routes";
import { OrderEditorPage } from "../pages/order-editor";
import { OrdersPage } from "../pages/orders";
import { QuoteEditorPage } from "../pages/quote-editor";
import { QuotesPage } from "../pages/quotes";
import { SettingsPage } from "../pages/settings";
import { TemplatesPage } from "../pages/templates";

/** Everything allowed, as an owner's session answers: the default of the page tests. */
export const fullAccess: OrdersAccess = {
  canManageQuotes: true,
  canManageOrders: true,
  canConfirmOrders: true,
  canManageSettings: true,
  canViewCustomers: true,
  canViewContacts: true,
  canSearchProducts: true,
  canReadVatCodes: true,
  canViewProjects: true,
  canListChannels: true,
  userId: "00000000-0000-4000-8000-000000000001",
  userDisplayName: "Kari Selger",
};

export { noOrdersAccess };

const searchCustomer = (search: Record<string, unknown>) => {
  const id = Number(search.customerId);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
};

/**
 * Stands in for the host's routes: the same paths, each page handed the
 * access the test names — as the host's routes hand theirs — so a test
 * drives a real router and reads the URL back rather than mocking it.
 */
const routeTree = (access: OrdersAccess, extra?: ReactNode) => {
  const root = createRootRoute({ component: () => <Outlet /> });
  const route = (path: string, component: () => ReactNode) =>
    createRoute({ getParentRoute: () => root, path, component });
  const QuoteRoute = () => {
    const { quoteId } = quoteRoute.useParams();
    return <QuoteEditorPage quoteId={Number(quoteId)} access={access} />;
  };
  const OrderRoute = () => {
    const { orderId } = orderRoute.useParams();
    return <OrderEditorPage orderId={Number(orderId)} access={access} />;
  };
  const quoteRoute = createRoute({ getParentRoute: () => root, path: QUOTE_ROUTE_PATH, component: QuoteRoute });
  const orderRoute = createRoute({ getParentRoute: () => root, path: SALES_ORDER_ROUTE_PATH, component: OrderRoute });
  const newQuote = createRoute({
    getParentRoute: () => root,
    path: NEW_QUOTE_ROUTE_PATH,
    validateSearch: (search: Record<string, unknown>) => ({ customerId: searchCustomer(search) }),
    component: () => <QuoteEditorPage customerId={newQuote.useSearch().customerId} access={access} />,
  });
  const newOrder = createRoute({
    getParentRoute: () => root,
    path: NEW_SALES_ORDER_ROUTE_PATH,
    validateSearch: (search: Record<string, unknown>) => ({ customerId: searchCustomer(search) }),
    component: () => <OrderEditorPage customerId={newOrder.useSearch().customerId} access={access} />,
  });
  return root.addChildren([
    route(QUOTES_ROUTE_PATH, () => <QuotesPage access={access} />),
    newQuote,
    quoteRoute,
    route(SALES_ORDERS_ROUTE_PATH, () => <OrdersPage access={access} />),
    newOrder,
    orderRoute,
    route(TEMPLATES_ROUTE_PATH, () => <TemplatesPage access={access} />),
    route(SETTINGS_ROUTE_PATH, () => <SettingsPage access={access} />),
    route("/here", () => extra),
  ]);
};

const mount = (url: string, access: OrdersAccess, extra?: ReactNode) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: routeTree(access, extra),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  render(
    <MantineProvider env="test">
      <Notifications />
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { router, queryClient };
};

/** Mounts the stand-in routes at a URL, with everything allowed unless the test narrows it. */
export const renderRoute = (url: string, access: Partial<OrdersAccess> = {}) =>
  mount(url, { ...fullAccess, ...access });

/** Mounts the customer page's panel at `/here`, beside the editors its buttons and rows lead to. */
export const renderPanel = (props: { customerId: number; canCreateQuote: boolean; canCreateOrder: boolean }) =>
  mount("/here", fullAccess, <CustomerOrdersPanel {...props} />);
