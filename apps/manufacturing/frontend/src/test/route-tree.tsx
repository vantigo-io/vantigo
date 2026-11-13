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
  BOM_OVERVIEW_ROUTE_PATH,
  BOM_ROUTE_PATH,
  BOMS_ROUTE_PATH,
  DEVIATION_ROUTE_PATH,
  DEVIATIONS_ROUTE_PATH,
  MANUFACTURING_HOME_ROUTE_PATH,
  MY_WORK_ROUTE_PATH,
  NEW_BOM_ROUTE_PATH,
  NEW_ORDER_ROUTE_PATH,
  NEW_WORK_CENTER_ROUTE_PATH,
  ORDER_ROUTE_PATH,
  ORDERS_ROUTE_PATH,
  REPORTS_ROUTE_PATH,
  SCHEDULE_ROUTE_PATH,
  SHOP_FLOOR_ROUTE_PATH,
  SHOP_FLOOR_WORK_CENTER_ROUTE_PATH,
  SUGGESTIONS_ROUTE_PATH,
  UNBUILD_ROUTE_PATH,
  UNBUILDS_ROUTE_PATH,
  WORK_CENTER_ROUTE_PATH,
  WORK_CENTERS_ROUTE_PATH,
  WORK_ORDER_ROUTE_PATH,
} from "../lib/routes";
import {
  validateBomOverviewSearch,
  validateBomsSearch,
  validateDeviationsSearch,
  validateNewOrderSearch,
  validateOrdersSearch,
  validateWorkCentersSearch,
} from "../lib/search";
import { BomPage, NewBomPage } from "../pages/bom";
import { BomOverviewPage } from "../pages/bom-overview";
import { BomsPage } from "../pages/boms";
import { DeviationPage } from "../pages/deviation";
import { DeviationsPage } from "../pages/deviations";
import { MyWorkPage } from "../pages/my-work";
import { NewOrderPage, OrderPage } from "../pages/order";
import { OrdersPage } from "../pages/orders";
import { ManufacturingOverviewPage } from "../pages/overview";
import { ReportsPage } from "../pages/reports";
import { SchedulePage } from "../pages/schedule";
import { ShopFloorPage, ShopFloorWorkCenterPage } from "../pages/shop-floor";
import { SuggestionsPage } from "../pages/suggestions";
import { UnbuildPage } from "../pages/unbuild";
import { UnbuildsPage } from "../pages/unbuilds";
import { NewWorkCenterPage, WorkCenterPage } from "../pages/work-center";
import { WorkCentersPage } from "../pages/work-centers";
import { WorkOrderPage } from "../pages/work-order";
import "../i18n";

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
    route(MANUFACTURING_HOME_ROUTE_PATH, ManufacturingOverviewPage),
    route(ORDERS_ROUTE_PATH, OrdersPage, validateOrdersSearch),
    route(NEW_ORDER_ROUTE_PATH, NewOrderPage, validateNewOrderSearch),
    route(ORDER_ROUTE_PATH, OrderPage),
    route(BOMS_ROUTE_PATH, BomsPage, validateBomsSearch),
    route(NEW_BOM_ROUTE_PATH, NewBomPage),
    route(BOM_ROUTE_PATH, BomPage),
    route(BOM_OVERVIEW_ROUTE_PATH, BomOverviewPage, validateBomOverviewSearch),
    route(WORK_CENTERS_ROUTE_PATH, WorkCentersPage, validateWorkCentersSearch),
    route(NEW_WORK_CENTER_ROUTE_PATH, NewWorkCenterPage),
    route(WORK_CENTER_ROUTE_PATH, WorkCenterPage),
    route(SHOP_FLOOR_ROUTE_PATH, ShopFloorPage),
    route(MY_WORK_ROUTE_PATH, MyWorkPage),
    route(SHOP_FLOOR_WORK_CENTER_ROUTE_PATH, ShopFloorWorkCenterPage),
    route(WORK_ORDER_ROUTE_PATH, WorkOrderPage),
    route(SCHEDULE_ROUTE_PATH, SchedulePage),
    route(REPORTS_ROUTE_PATH, ReportsPage),
    route(DEVIATIONS_ROUTE_PATH, DeviationsPage, validateDeviationsSearch),
    route(DEVIATION_ROUTE_PATH, DeviationPage),
    route(SUGGESTIONS_ROUTE_PATH, SuggestionsPage),
    route(UNBUILDS_ROUTE_PATH, UnbuildsPage),
    route(UNBUILD_ROUTE_PATH, UnbuildPage),
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
