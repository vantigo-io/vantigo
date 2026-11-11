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
  MANUFACTURING_HOME_ROUTE_PATH,
  NEW_BOM_ROUTE_PATH,
  NEW_WORK_CENTER_ROUTE_PATH,
  WORK_CENTER_ROUTE_PATH,
  WORK_CENTERS_ROUTE_PATH,
} from "../lib/routes";
import { validateBomOverviewSearch, validateBomsSearch, validateWorkCentersSearch } from "../lib/search";
import { BomPage, NewBomPage } from "../pages/bom";
import { BomOverviewPage } from "../pages/bom-overview";
import { BomsPage } from "../pages/boms";
import { ManufacturingOverviewPage } from "../pages/overview";
import { NewWorkCenterPage, WorkCenterPage } from "../pages/work-center";
import { WorkCentersPage } from "../pages/work-centers";
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
    route(BOMS_ROUTE_PATH, BomsPage, validateBomsSearch),
    route(NEW_BOM_ROUTE_PATH, NewBomPage),
    route(BOM_ROUTE_PATH, BomPage),
    route(BOM_OVERVIEW_ROUTE_PATH, BomOverviewPage, validateBomOverviewSearch),
    route(WORK_CENTERS_ROUTE_PATH, WorkCentersPage, validateWorkCentersSearch),
    route(NEW_WORK_CENTER_ROUTE_PATH, NewWorkCenterPage),
    route(WORK_CENTER_ROUTE_PATH, WorkCenterPage),
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
