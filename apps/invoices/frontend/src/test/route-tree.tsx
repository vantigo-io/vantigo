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
import "../i18n";
import { INVOICE_ROUTE_PATH } from "../lib/routes";
import { InvoicesPage } from "../pages/invoices";
import { InvoiceRoute } from "./invoice-route";

/**
 * Stands in for the host routes this package's pages are mounted by: the
 * same paths, so a page test drives a real router and reads the URL back
 * instead of mocking navigation.
 */
const makeRouteTree = (canViewCustomers: boolean) => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });
  return rootRoute.addChildren([
    createRoute({
      getParentRoute: () => rootRoute,
      path: "/invoices",
      component: () => <InvoicesPage canViewCustomers={canViewCustomers} userDisplayName="Ola Nordmann" />,
    }),
    createRoute({
      getParentRoute: () => rootRoute,
      path: INVOICE_ROUTE_PATH,
      component: () => <InvoiceRoute canViewCustomers={canViewCustomers} />,
    }),
  ]);
};

/** Mounts the stand-in routes at a URL under the providers the host gives the pages. */
export const renderRoute = (url: string, { canViewCustomers = true }: { canViewCustomers?: boolean } = {}) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const router = createRouter({
    routeTree: makeRouteTree(canViewCustomers),
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
