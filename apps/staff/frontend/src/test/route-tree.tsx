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
import { MY_PAGE_ROUTE_PATH, PEOPLE_ROUTE_PATH, PERSON_ROUTE_PATH, STAFF_SETTINGS_ROUTE_PATH } from "../lib/routes";
import { validatePeopleSearch, validatePersonSearch } from "../lib/search";
import { MyPage } from "../pages/my-page";
import { PeoplePage } from "../pages/people";
import { PersonPage } from "../pages/person";
import { SettingsPage } from "../pages/settings";
import "../i18n";

/**
 * Stands in for the host routes this package's pages are mounted by (Task
 * 6): the same paths — the package's own constants — and the same
 * `validateSearch`, so a page test drives a real router and reads the URL
 * back instead of mocking navigation. A fresh tree per call: a route carries
 * its children, and tests mount it more than once.
 */
export const makeRouteTree = () => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => <Outlet />,
  });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: PEOPLE_ROUTE_PATH,
      component: PeoplePage,
      validateSearch: validatePeopleSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: PERSON_ROUTE_PATH,
      component: PersonPage,
      validateSearch: validatePersonSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: MY_PAGE_ROUTE_PATH,
      component: MyPage,
      validateSearch: validatePersonSearch,
    }),
    createRoute({ getParentRoute: () => rootRoute, path: STAFF_SETTINGS_ROUTE_PATH, component: SettingsPage }),
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
