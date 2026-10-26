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
  BOARD_ROUTE_PATH,
  INTAKE_ROUTE_PATH,
  MY_PLANNING_ROUTE_PATH,
  PERSON_TIMELINE_ROUTE_PATH,
  PLANNING_SETTINGS_ROUTE_PATH,
  PROJECT_PLAN_ROUTE_PATH,
  REQUESTS_ROUTE_PATH,
  UNBOOKED_ROUTE_PATH,
  WISHES_ROUTE_PATH,
} from "../lib/routes";
import { validateBoardSearch, validateRequestsSearch, validateWindowSearch } from "../lib/search";
import { BoardPage } from "../pages/board";
import { IntakePage } from "../pages/intake";
import { MyPlanningPage } from "../pages/me";
import { PersonTimelinePage } from "../pages/person-timeline";
import { ProjectPlanPage } from "../pages/project-plan";
import { RequestsPage } from "../pages/requests";
import { PlanningSettingsPage } from "../pages/settings";
import { UnbookedPage } from "../pages/unbooked";
import { WishesPage } from "../pages/wishes";
import "../i18n";
import { ProjectPlanningTab } from "./project-planning-tab";
import { StaffPersonSlot } from "./staff-person-slot";

/** Where the host puts `PersonTimeline` on Staff's person page (`StaffPersonSlot`). */
export const STAFF_PERSON_TEST_PATH = "/staff/people/$id";
/** Where the host puts `ProjectPlan` on the project page's Planning tab (`ProjectPlanningTab`). */
export const PROJECT_TAB_TEST_PATH = "/projects/$projectId/planning";

/**
 * Stands in for the host routes this package's pages are mounted by: the same
 * paths — the package's own constants — and the same `validateSearch`, so a
 * page test drives a real router and reads the URL back instead of mocking
 * navigation. A fresh tree per call: a route carries its children, and tests
 * mount it more than once.
 */
export const makeRouteTree = () => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => <Outlet />,
  });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: BOARD_ROUTE_PATH,
      component: BoardPage,
      validateSearch: validateBoardSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: PROJECT_PLAN_ROUTE_PATH,
      component: ProjectPlanPage,
      validateSearch: validateWindowSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: PERSON_TIMELINE_ROUTE_PATH,
      component: PersonTimelinePage,
      validateSearch: validateWindowSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: REQUESTS_ROUTE_PATH,
      component: RequestsPage,
      validateSearch: validateRequestsSearch,
    }),
    createRoute({ getParentRoute: () => rootRoute, path: INTAKE_ROUTE_PATH, component: IntakePage }),
    createRoute({ getParentRoute: () => rootRoute, path: UNBOOKED_ROUTE_PATH, component: UnbookedPage }),
    createRoute({ getParentRoute: () => rootRoute, path: WISHES_ROUTE_PATH, component: WishesPage }),
    createRoute({ getParentRoute: () => rootRoute, path: MY_PLANNING_ROUTE_PATH, component: MyPlanningPage }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: PLANNING_SETTINGS_ROUTE_PATH,
      component: PlanningSettingsPage,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: STAFF_PERSON_TEST_PATH,
      component: StaffPersonSlot,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: PROJECT_TAB_TEST_PATH,
      component: ProjectPlanningTab,
    }),
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
