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
  ACCOUNTING_HOME_ROUTE_PATH,
  ACCOUNTS_ROUTE_PATH,
  JOURNAL_ROUTE_PATH,
  OPEN_ITEMS_ROUTE_PATH,
  OPENING_ROUTE_PATH,
  REPORTS_ROUTE_PATH,
  SETTINGS_ROUTE_PATH,
  SETUP_ROUTE_PATH,
  TAX_CODES_ROUTE_PATH,
  VOUCHER_ROUTE_PATH,
  VOUCHERS_ROUTE_PATH,
} from "../lib/routes";
import { validateJournalSearch } from "../lib/search";
import { AccountsPage } from "../pages/accounts";
import { JournalEntryPage } from "../pages/journal";
import { OpenItemsPage } from "../pages/open-items";
import { OpeningPage } from "../pages/opening";
import { AccountingOverviewPage } from "../pages/overview";
import { ReportsPage } from "../pages/reports";
import { SettingsPage } from "../pages/settings";
import { SetupPage } from "../pages/setup";
import { TaxCodesPage } from "../pages/tax-codes";
import { VoucherPage } from "../pages/voucher";
import { VouchersPage } from "../pages/vouchers";
import "../i18n";

/**
 * Stands in for the host routes this package's pages are mounted by: the same
 * paths — the package's own constants — and the same `validateSearch`.
 */
export const makeRouteTree = () => {
  const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({ component: () => <Outlet /> });
  const route = (
    path: string,
    component: () => React.ReactNode,
    validateSearch?: (s: Record<string, unknown>) => object,
  ) => createRoute({ getParentRoute: () => rootRoute, path, component, ...(validateSearch ? { validateSearch } : {}) });
  return rootRoute.addChildren([
    route(ACCOUNTING_HOME_ROUTE_PATH, AccountingOverviewPage),
    route(SETUP_ROUTE_PATH, SetupPage),
    route(VOUCHERS_ROUTE_PATH, VouchersPage),
    route(VOUCHER_ROUTE_PATH, VoucherPage),
    route(JOURNAL_ROUTE_PATH, JournalEntryPage, validateJournalSearch),
    route(ACCOUNTS_ROUTE_PATH, AccountsPage),
    route(TAX_CODES_ROUTE_PATH, TaxCodesPage),
    route(OPEN_ITEMS_ROUTE_PATH, OpenItemsPage),
    route(OPENING_ROUTE_PATH, OpeningPage),
    route(REPORTS_ROUTE_PATH, ReportsPage),
    route(SETTINGS_ROUTE_PATH, SettingsPage),
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
