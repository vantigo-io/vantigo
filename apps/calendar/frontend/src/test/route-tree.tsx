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
import type { ReactNode } from "react";
import { CALENDAR_ROUTE_PATH } from "../lib/routes";
import { validateCalendarSearch } from "../lib/search";
import { CalendarPage } from "../pages/calendar";
import "../i18n";

/**
 * Stands in for the host's routes: the calendar at its own path with the
 * package's `validateSearch`, a dashboard at `/` showing whatever the test
 * puts there, and a catch-all so a link to another module's record lands
 * somewhere the test can read back from the router.
 */
const makeRouteTree = (home: ReactNode) => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });
  return rootRoute.addChildren([
    createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => <>{home}</> }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: CALENDAR_ROUTE_PATH,
      component: CalendarPage,
      validateSearch: validateCalendarSearch,
    }),
    createRoute({ getParentRoute: () => rootRoute, path: "$", component: () => null }),
  ]);
};

/** Mounts the route tree at a URL under the providers the host gives the pages. */
export const renderRoute = (url: string, home: ReactNode = null) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(home),
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  const result = render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { ...result, router, queryClient };
};
