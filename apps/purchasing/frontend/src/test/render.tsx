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
import { ProductSuppliersCard } from "../components/product-suppliers-card";
import type { PurchasingAccess } from "../lib/access";
import {
  NEW_PURCHASE_ORDER_ROUTE_PATH,
  PURCHASE_ORDER_ROUTE_PATH,
  PURCHASE_ORDERS_ROUTE_PATH,
  SETTINGS_ROUTE_PATH,
  SUPPLIER_ROUTE_PATH,
  SUPPLIERS_ROUTE_PATH,
  validateNewPurchaseOrderSearch,
} from "../lib/routes";
import { NewPurchaseOrderPage, PurchaseOrderPage } from "../pages/order";
import { PurchaseOrdersPage } from "../pages/orders";
import { PurchasingSettingsPage } from "../pages/settings";
import { type RenderSupplierForm, SupplierPage, SuppliersPage } from "../pages/suppliers";

/** Everything allowed, as an owner's session answers: the default of the page tests. */
export const fullAccess: PurchasingAccess = {
  canManageOrders: true,
  canSendOrders: true,
  canManageSupplierItems: true,
  canManageSettings: true,
  userId: "00000000-0000-4000-8000-000000000001",
};

/** A stand-in for the host's composed supplier form (Accounting's), which says what it was handed. */
export const fakeSupplierForm: RenderSupplierForm = ({ supplierId, onSaved }) => (
  <div data-testid="supplier-form">
    {supplierId === undefined ? "new supplier form" : `supplier form ${supplierId}`}
    <button type="button" onClick={() => onSaved(supplierId ?? 77)}>
      save supplier
    </button>
  </div>
);

/**
 * Stands in for the host's routes: the same paths, each page handed the
 * access the test names — as the host's routes hand theirs — so a test drives
 * a real router and reads the URL back rather than mocking it.
 */
const routeTree = (access: PurchasingAccess, renderSupplierForm?: RenderSupplierForm, extra?: ReactNode) => {
  const root = createRootRoute({ component: () => <Outlet /> });
  const route = (path: string, component: () => ReactNode) =>
    createRoute({ getParentRoute: () => root, path, component });
  const orderRoute = createRoute({
    getParentRoute: () => root,
    path: PURCHASE_ORDER_ROUTE_PATH,
    component: () => <PurchaseOrderPage orderId={Number(orderRoute.useParams().orderId)} access={access} />,
  });
  const newOrder = createRoute({
    getParentRoute: () => root,
    path: NEW_PURCHASE_ORDER_ROUTE_PATH,
    validateSearch: validateNewPurchaseOrderSearch,
    component: () => <NewPurchaseOrderPage supplierId={newOrder.useSearch().supplierId} access={access} />,
  });
  const supplierRoute = createRoute({
    getParentRoute: () => root,
    path: SUPPLIER_ROUTE_PATH,
    component: () => (
      <SupplierPage
        supplierId={Number(supplierRoute.useParams().supplierId)}
        access={access}
        renderSupplierForm={renderSupplierForm}
      />
    ),
  });
  return root.addChildren([
    route(PURCHASE_ORDERS_ROUTE_PATH, () => <PurchaseOrdersPage access={access} />),
    newOrder,
    orderRoute,
    route(SUPPLIERS_ROUTE_PATH, () => <SuppliersPage renderSupplierForm={renderSupplierForm} />),
    supplierRoute,
    route(SETTINGS_ROUTE_PATH, () => <PurchasingSettingsPage access={access} />),
    route("/here", () => extra),
    // Other apps' routes the pages link to.
    route("/accounting/suppliers/$supplierId", () => <p>Accounting's supplier</p>),
    route("/products/$productId", () => <p>A product</p>),
  ]);
};

const mount = (url: string, access: PurchasingAccess, renderSupplierForm?: RenderSupplierForm, extra?: ReactNode) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: routeTree(access, renderSupplierForm, extra),
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
export const renderRoute = (
  url: string,
  access: Partial<PurchasingAccess> = {},
  renderSupplierForm: RenderSupplierForm | undefined = fakeSupplierForm,
) => mount(url, { ...fullAccess, ...access }, renderSupplierForm);

/** Mounts the product page's card at `/here`, as the host composes it. */
export const renderProductSuppliers = (variantIds: number[]) =>
  mount("/here", fullAccess, undefined, <ProductSuppliersCard variantIds={variantIds} />);
