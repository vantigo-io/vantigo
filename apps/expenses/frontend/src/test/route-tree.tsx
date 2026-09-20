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
import { CLAIM_ROUTE_PATH } from "../lib/routes";
import { validateApprovalsSearch, validateMyExpensesSearch, validateReimbursementsSearch } from "../lib/search";
import { ApprovalsPage } from "../pages/approvals";
import { MyExpensesPage } from "../pages/my-expenses";
import { ReimbursementsPage } from "../pages/reimbursements";
import { SettingsPage } from "../pages/settings";
import { ClaimRoute } from "./claim-route";
import "../i18n";
import { ME } from "./fixtures";

/**
 * Stands in for the host routes this package's pages are mounted by (Task 8):
 * the same path and the same `validateSearch` the host will use — the
 * package's own exported validator — so a page test drives a real router and
 * reads the URL back instead of mocking navigation. A fresh tree per call: a
 * route carries its children, and tests mount it more than once.
 */
export const makeRouteTree = (userId: string) => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
    component: () => <Outlet />,
  });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/expenses",
      component: () => <MyExpensesPage userId={userId} />,
      validateSearch: validateMyExpensesSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/expenses/approvals",
      component: ApprovalsPage,
      validateSearch: validateApprovalsSearch,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/expenses/reimbursements",
      component: ReimbursementsPage,
      validateSearch: validateReimbursementsSearch,
    }),
    createRoute({ getParentRoute: () => rootRoute, path: "/expenses/settings", component: SettingsPage }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: CLAIM_ROUTE_PATH,
      component: ClaimRoute,
    }),
  ]);
};

/** Mounts the stand-in route at a URL under the providers the host gives the pages. */
export const renderRoute = (url: string, { userId = ME }: { userId?: string } = {}) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(userId),
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
