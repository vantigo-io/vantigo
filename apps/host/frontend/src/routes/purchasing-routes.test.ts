import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound } from "@tanstack/react-router";
import {
  NEW_PURCHASE_ORDER_ROUTE_PATH,
  PURCHASE_ORDER_ROUTE_PATH,
  PURCHASE_ORDERS_ROUTE_PATH,
  PURCHASING_HOME_ROUTE_PATH,
  purchasingNavItems,
  SETTINGS_ROUTE_PATH,
  SUPPLIER_ROUTE_PATH,
  SUPPLIERS_ROUTE_PATH,
} from "@vantigo/purchasing-ui";
import { describe, expect, it } from "vitest";
import { routeTree } from "../routeTree.gen";
import { Route as OrderRoute } from "./purchasing/orders.$orderId";
import { Route as SupplierRoute } from "./purchasing/suppliers.$supplierId";

const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId.replace(/(.)\/$/, "$1"));
};

const refusal = (route: { options: { beforeLoad?: unknown } }, params: Record<string, string>) => {
  const beforeLoad = route.options.beforeLoad as unknown as (context: unknown) => void;
  try {
    beforeLoad({ params });
  } catch (error) {
    return error;
  }
  return undefined;
};

/**
 * The Purchasing package writes its paths once (`lib/routes.ts`); the host
 * mounts a file route at each, under the app's layout.
 */
describe("the Purchasing routes", () => {
  it("mounts every path the package names under the layout", () => {
    for (const path of [
      PURCHASING_HOME_ROUTE_PATH,
      PURCHASE_ORDERS_ROUTE_PATH,
      NEW_PURCHASE_ORDER_ROUTE_PATH,
      PURCHASE_ORDER_ROUTE_PATH,
      SUPPLIERS_ROUTE_PATH,
      SUPPLIER_ROUTE_PATH,
      SETTINGS_ROUTE_PATH,
    ]) {
      const concrete = path.replace("$orderId", "7").replace("$supplierId", "40");
      const ids = matchedRouteIds(concrete);
      expect(ids, concrete).toContain("/purchasing");
      expect(ids.at(-1), concrete).toBe(path);
    }
    for (const item of purchasingNavItems) expect(matchedRouteIds(item.to)).toContain(item.to);
  });

  it("answers not found for an order or a supplier id that is not a positive whole number", () => {
    expect(isNotFound(refusal(OrderRoute, { orderId: "PO-3001" }))).toBe(true);
    expect(refusal(OrderRoute, { orderId: "7" })).toBeUndefined();
    expect(isNotFound(refusal(SupplierRoute, { supplierId: "0" }))).toBe(true);
    expect(refusal(SupplierRoute, { supplierId: "40" })).toBeUndefined();
  });
});
