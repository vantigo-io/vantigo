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
import { isIsoDate } from "../lib/week";
import { DayPage, type DaySearch } from "../pages/day";
import { MyWeekPage, type MyWeekSearch } from "../pages/my-week";
import "../i18n";

/**
 * Stands in for the host routes the package's pages are mounted by (Task 7):
 * the same paths and the same `validateSearch`, so a page test drives a real
 * router and reads the URL back instead of mocking navigation. A fresh tree
 * per call: a route carries its children, and tests mount it more than once.
 */
export const makeRouteTree = () => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => <Outlet />,
  });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/time",
      component: MyWeekPage,
      validateSearch: (search: Record<string, unknown>): MyWeekSearch => ({
        week: isIsoDate(search.week) ? search.week : undefined,
      }),
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/time/day",
      component: DayPage,
      validateSearch: (search: Record<string, unknown>): DaySearch => ({
        date: isIsoDate(search.date) ? search.date : undefined,
      }),
    }),
  ]);
};

/** Mounts the stand-in routes at a URL under the providers the host gives the pages. */
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
