import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound } from "@tanstack/react-router";
import {
  ACCOUNTING_HOME_ROUTE_PATH,
  ACCOUNTS_ROUTE_PATH,
  accountingNavItems,
  JOURNAL_ROUTE_PATH,
  OPEN_ITEMS_ROUTE_PATH,
  OPENING_ROUTE_PATH,
  REPORTS_ROUTE_PATH,
  SETTINGS_ROUTE_PATH,
  SETUP_ROUTE_PATH,
  TAX_CODES_ROUTE_PATH,
  VOUCHER_ROUTE_PATH,
  VOUCHERS_ROUTE_PATH,
} from "@vantigo/accounting-ui";
import { describe, expect, it } from "vitest";
import { routeTree } from "../routeTree.gen";
import { Route as VoucherRoute } from "./accounting/vouchers.$voucherId";

const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId.replace(/(.)\/$/, "$1"));
};

/**
 * The Accounting package writes its paths once (`lib/routes.ts`); the host
 * mounts a file route at each, under the app's layout.
 */
describe("the Accounting routes", () => {
  it("mounts every path the package names under the layout", () => {
    for (const path of [
      ACCOUNTING_HOME_ROUTE_PATH,
      SETUP_ROUTE_PATH,
      VOUCHERS_ROUTE_PATH,
      VOUCHER_ROUTE_PATH,
      JOURNAL_ROUTE_PATH,
      ACCOUNTS_ROUTE_PATH,
      TAX_CODES_ROUTE_PATH,
      OPEN_ITEMS_ROUTE_PATH,
      OPENING_ROUTE_PATH,
      REPORTS_ROUTE_PATH,
      SETTINGS_ROUTE_PATH,
    ]) {
      const concrete = path.replace("$voucherId", "7");
      const ids = matchedRouteIds(concrete);
      expect(ids, concrete).toContain("/accounting");
      expect(ids.at(-1), concrete).toBe(path);
    }
    for (const item of accountingNavItems) expect(matchedRouteIds(item.to)).toContain(item.to);
  });

  it("answers not found for a voucher id that is not a positive whole number", () => {
    const beforeLoad = VoucherRoute.options.beforeLoad as unknown as (context: unknown) => void;
    const run = (voucherId: string) => {
      try {
        beforeLoad({ params: { voucherId } });
      } catch (error) {
        return error;
      }
      return undefined;
    };
    expect(isNotFound(run("2026-000001"))).toBe(true);
    expect(run("7")).toBeUndefined();
  });
});
