import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound } from "@tanstack/react-router";
import {
  BOM_OVERVIEW_ROUTE_PATH,
  BOM_ROUTE_PATH,
  BOMS_ROUTE_PATH,
  DEVIATION_ROUTE_PATH,
  DEVIATIONS_ROUTE_PATH,
  MANUFACTURING_HOME_ROUTE_PATH,
  MY_WORK_ROUTE_PATH,
  manufacturingNavItems,
  NEW_BOM_ROUTE_PATH,
  NEW_ORDER_ROUTE_PATH,
  NEW_WORK_CENTER_ROUTE_PATH,
  ORDER_ROUTE_PATH,
  ORDERS_ROUTE_PATH,
  REPORTS_ROUTE_PATH,
  SCHEDULE_ROUTE_PATH,
  SHOP_FLOOR_ROUTE_PATH,
  SHOP_FLOOR_WORK_CENTER_ROUTE_PATH,
  SUGGESTIONS_ROUTE_PATH,
  UNBUILD_ROUTE_PATH,
  UNBUILDS_ROUTE_PATH,
  WORK_CENTER_ROUTE_PATH,
  WORK_CENTERS_ROUTE_PATH,
  WORK_ORDER_ROUTE_PATH,
} from "@vantigo/manufacturing-ui";
import { describe, expect, it } from "vitest";
import { routeTree } from "../routeTree.gen";
import { Route as BomRoute } from "./manufacturing/boms.$bomId.index";
import { Route as BomOverviewRoute } from "./manufacturing/boms.$bomId.overview";
import { Route as DeviationRoute } from "./manufacturing/deviations.$deviationId";
import { Route as OrderRoute } from "./manufacturing/orders.$orderId";
import { Route as ShopFloorWorkCenterRoute } from "./manufacturing/shop-floor.work-centers.$workCenterId";
import { Route as WorkOrderRoute } from "./manufacturing/shop-floor.work-orders.$workOrderId";
import { Route as UnbuildRoute } from "./manufacturing/unbuilds.$unbuildId";
import { Route as WorkCenterRoute } from "./manufacturing/work-centers.$workCenterId";

/** Drives a route's `beforeLoad` with the params given; returns what it throws. */
const runBeforeLoad = (route: { options: { beforeLoad?: unknown } }, params: Record<string, string>) => {
  const beforeLoad = route.options.beforeLoad as (context: unknown) => void;
  try {
    beforeLoad({ params });
  } catch (error) {
    return error;
  }
  return undefined;
};

/** The route ids the generated tree matches for a path, through the real router, trailing slashes dropped. */
const matchedRouteIds = (pathname: string) => {
  const router = createRouter({
    routeTree,
    context: { queryClient: new QueryClient() },
    history: createMemoryHistory({ initialEntries: [pathname] }),
  });
  return router.matchRoutes(pathname, {}).map((match) => match.routeId.replace(/(.)\/$/, "$1"));
};

/**
 * The Manufacturing package writes its paths once (`lib/routes.ts`); the host
 * mounts a file route at each, under the app's layout.
 */
describe("the Manufacturing routes", () => {
  it("mounts every path the package names under the layout, a new record before an id", () => {
    for (const path of [
      MANUFACTURING_HOME_ROUTE_PATH,
      ORDERS_ROUTE_PATH,
      NEW_ORDER_ROUTE_PATH,
      ORDER_ROUTE_PATH,
      BOMS_ROUTE_PATH,
      NEW_BOM_ROUTE_PATH,
      BOM_ROUTE_PATH,
      BOM_OVERVIEW_ROUTE_PATH,
      WORK_CENTERS_ROUTE_PATH,
      NEW_WORK_CENTER_ROUTE_PATH,
      WORK_CENTER_ROUTE_PATH,
      SHOP_FLOOR_ROUTE_PATH,
      MY_WORK_ROUTE_PATH,
      SHOP_FLOOR_WORK_CENTER_ROUTE_PATH,
      WORK_ORDER_ROUTE_PATH,
      SCHEDULE_ROUTE_PATH,
      REPORTS_ROUTE_PATH,
      DEVIATIONS_ROUTE_PATH,
      DEVIATION_ROUTE_PATH,
      SUGGESTIONS_ROUTE_PATH,
      UNBUILDS_ROUTE_PATH,
      UNBUILD_ROUTE_PATH,
    ]) {
      const concrete = path
        .replace("$bomId", "7")
        .replace("$workCenterId", "3")
        .replace("$orderId", "5")
        .replace("$workOrderId", "40")
        .replace("$deviationId", "9")
        .replace("$unbuildId", "11");
      const ids = matchedRouteIds(concrete);
      expect(ids, concrete).toContain("/manufacturing");
      expect(ids.at(-1), concrete).toBe(path);
    }
    for (const item of manufacturingNavItems) expect(matchedRouteIds(item.to)).toContain(item.to);
  });

  it("answers not found for an id that is not a positive whole number", () => {
    expect(isNotFound(runBeforeLoad(BomRoute, { bomId: "abc" }))).toBe(true);
    expect(isNotFound(runBeforeLoad(BomOverviewRoute, { bomId: "0" }))).toBe(true);
    expect(isNotFound(runBeforeLoad(WorkCenterRoute, { workCenterId: "-1" }))).toBe(true);
    expect(isNotFound(runBeforeLoad(OrderRoute, { orderId: "MO-12" }))).toBe(true);
    expect(runBeforeLoad(OrderRoute, { orderId: "5" })).toBeUndefined();
    expect(isNotFound(runBeforeLoad(ShopFloorWorkCenterRoute, { workCenterId: "mine" }))).toBe(true);
    expect(isNotFound(runBeforeLoad(WorkOrderRoute, { workOrderId: "0" }))).toBe(true);
    expect(runBeforeLoad(ShopFloorWorkCenterRoute, { workCenterId: "3" })).toBeUndefined();
    expect(runBeforeLoad(WorkOrderRoute, { workOrderId: "40" })).toBeUndefined();
    expect(runBeforeLoad(BomRoute, { bomId: "7" })).toBeUndefined();
    expect(runBeforeLoad(WorkCenterRoute, { workCenterId: "3" })).toBeUndefined();
    expect(isNotFound(runBeforeLoad(DeviationRoute, { deviationId: "DEV-9" }))).toBe(true);
    expect(runBeforeLoad(DeviationRoute, { deviationId: "9" })).toBeUndefined();
    expect(isNotFound(runBeforeLoad(UnbuildRoute, { unbuildId: "UB-1" }))).toBe(true);
    expect(runBeforeLoad(UnbuildRoute, { unbuildId: "11" })).toBeUndefined();
  });
});
