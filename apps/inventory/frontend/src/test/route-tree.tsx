import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import {
  COUNT_ROUTE_PATH,
  COUNTS_ROUTE_PATH,
  IMPORT_ROUTE_PATH,
  INVENTORY_HOME_ROUTE_PATH,
  LOCATION_ROUTE_PATH,
  LOOKUP_ROUTE_PATH,
  LOT_ROUTE_PATH,
  LOTS_ROUTE_PATH,
  LOW_STOCK_ROUTE_PATH,
  MOVEMENT_ROUTE_PATH,
  MOVEMENTS_ROUTE_PATH,
  NEW_ADJUSTMENT_ROUTE_PATH,
  NEW_MOVE_ROUTE_PATH,
  NEW_RECEIPT_ROUTE_PATH,
  NEW_TRANSFER_ROUTE_PATH,
  RECEIVE_TRANSFER_ROUTE_PATH,
  SITE_ROUTE_PATH,
  TRANSFER_ROUTE_PATH,
  TRANSFERS_ROUTE_PATH,
  VARIANT_STOCK_ROUTE_PATH,
} from "../lib/routes";
import {
  validateCountsSearch,
  validateLookupSearch,
  validateLotsSearch,
  validateLowStockSearch,
  validateMovementsSearch,
  validateNewMovementSearch,
  validateNewTransferSearch,
  validateTransfersSearch,
} from "../lib/search";
import { CountPage } from "../pages/count";
import { CountsPage } from "../pages/counts";
import { InventoryHomePage } from "../pages/home";
import { ImportPage } from "../pages/import";
import { LocationPage } from "../pages/location";
import { LookupPage } from "../pages/lookup";
import { LotPage } from "../pages/lot";
import { LotsPage } from "../pages/lots";
import { LowStockPage } from "../pages/low-stock";
import { MovementPage } from "../pages/movement";
import { MovementsPage } from "../pages/movements";
import { NewAdjustmentPage } from "../pages/new-adjustment";
import { NewMovePage } from "../pages/new-move";
import { NewReceiptPage } from "../pages/new-receipt";
import { NewTransferPage } from "../pages/new-transfer";
import { ReceiveTransferPage } from "../pages/receive-transfer";
import { SitePage } from "../pages/site";
import { TransferPage } from "../pages/transfer";
import { TransfersPage } from "../pages/transfers";
import { VariantStockPage } from "../pages/variant";
import "../i18n";
import { ProductSlot } from "./product-slot";

/** Where the host puts `ProductStock` on Products' product page. */
export const PRODUCT_TEST_PATH = "/products/$productId";

/**
 * Stands in for the host routes this package's pages are mounted by: the same
 * paths — the package's own constants — and the same `validateSearch`, so a
 * page test drives a real router and reads the URL back instead of mocking
 * navigation. A fresh tree per call: a route carries its children.
 */
export const makeRouteTree = () => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });
  const route = (
    path: string,
    component: () => React.ReactNode,
    validateSearch?: (s: Record<string, unknown>) => object,
  ) => createRoute({ getParentRoute: () => rootRoute, path, component, ...(validateSearch ? { validateSearch } : {}) });
  return rootRoute.addChildren([
    route(INVENTORY_HOME_ROUTE_PATH, InventoryHomePage),
    route(LOOKUP_ROUTE_PATH, LookupPage, validateLookupSearch),
    route(SITE_ROUTE_PATH, SitePage),
    route(LOCATION_ROUTE_PATH, LocationPage),
    route(VARIANT_STOCK_ROUTE_PATH, VariantStockPage),
    route(MOVEMENTS_ROUTE_PATH, MovementsPage, validateMovementsSearch),
    route(NEW_ADJUSTMENT_ROUTE_PATH, NewAdjustmentPage, validateNewMovementSearch),
    route(NEW_MOVE_ROUTE_PATH, NewMovePage, validateNewMovementSearch),
    route(NEW_RECEIPT_ROUTE_PATH, NewReceiptPage, validateNewMovementSearch),
    route(MOVEMENT_ROUTE_PATH, MovementPage),
    route(COUNTS_ROUTE_PATH, CountsPage, validateCountsSearch),
    route(COUNT_ROUTE_PATH, CountPage),
    route(IMPORT_ROUTE_PATH, ImportPage),
    route(TRANSFERS_ROUTE_PATH, TransfersPage, validateTransfersSearch),
    route(NEW_TRANSFER_ROUTE_PATH, NewTransferPage, validateNewTransferSearch),
    route(TRANSFER_ROUTE_PATH, TransferPage),
    route(RECEIVE_TRANSFER_ROUTE_PATH, ReceiveTransferPage),
    route(LOW_STOCK_ROUTE_PATH, LowStockPage, validateLowStockSearch),
    route(LOTS_ROUTE_PATH, LotsPage, validateLotsSearch),
    route(LOT_ROUTE_PATH, () => <LotPage />),
    route(PRODUCT_TEST_PATH, ProductSlot),
  ]);
};

/** Mounts the route tree at a URL under the providers the host gives the pages. */
export const renderRoute = (url: string) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(),
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { router, queryClient };
};
