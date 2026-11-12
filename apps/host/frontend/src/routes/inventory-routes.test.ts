import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, isNotFound } from "@tanstack/react-router";
import {
  COUNT_ROUTE_PATH,
  COUNTS_ROUTE_PATH,
  countLinkOptions,
  EXPIRING_ROUTE_PATH,
  IMPORT_ROUTE_PATH,
  INVENTORY_HOME_ROUTE_PATH,
  inventoryNavItems,
  LOCATION_ROUTE_PATH,
  LOOKUP_ROUTE_PATH,
  LOT_ROUTE_PATH,
  MOVEMENT_ROUTE_PATH,
  MOVEMENTS_ROUTE_PATH,
  NEW_ADJUSTMENT_ROUTE_PATH,
  NEW_MOVE_ROUTE_PATH,
  NEW_RECEIPT_ROUTE_PATH,
  SITE_ROUTE_PATH,
  VARIANT_STOCK_ROUTE_PATH,
  validateLookupSearch,
  validateMovementsSearch,
  validateNewMovementSearch,
  variantStockHref,
} from "@vantigo/inventory-ui";
import { describe, expect, it } from "vitest";
import { routeTree } from "../routeTree.gen";
import { Route as InventoryRoute } from "./inventory";
import { Route as CountRoute } from "./inventory/counts.$countId";
import { Route as LocationRoute } from "./inventory/locations.$locationId";
import { Route as LookupRoute } from "./inventory/lookup";
import { Route as MovementRoute } from "./inventory/movements.$movementId";
import { Route as MovementsRoute } from "./inventory/movements.index";
import { Route as NewAdjustmentRoute } from "./inventory/movements.new.adjustment";
import { Route as NewMoveRoute } from "./inventory/movements.new.move";
import { Route as NewReceiptRoute } from "./inventory/movements.new.receipt";
import { Route as SiteRoute } from "./inventory/sites.$siteId";
import { Route as VariantRoute } from "./inventory/variants.$variantId";

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
 * The Inventory package writes its paths once (`lib/routes.ts`) and links to
 * them from its own pages; the host mounts them. A link that matches no
 * mounted route is a runtime error, so these pin that every path the package
 * names is one the generated tree serves.
 */
describe("the inventory routes", () => {
  it("mounts every path the package links to", () => {
    expect(matchedRouteIds(INVENTORY_HOME_ROUTE_PATH)).toContain(INVENTORY_HOME_ROUTE_PATH);
    expect(matchedRouteIds(LOOKUP_ROUTE_PATH)).toContain(LOOKUP_ROUTE_PATH);
    expect(matchedRouteIds("/inventory/sites/3")).toContain(SITE_ROUTE_PATH);
    expect(matchedRouteIds("/inventory/locations/12")).toContain(LOCATION_ROUTE_PATH);
    expect(matchedRouteIds("/inventory/variants/7")).toContain(VARIANT_STOCK_ROUTE_PATH);
    expect(matchedRouteIds(variantStockHref(7))).toContain(VARIANT_STOCK_ROUTE_PATH);
    expect(matchedRouteIds(MOVEMENTS_ROUTE_PATH)).toContain(MOVEMENTS_ROUTE_PATH);
    expect(matchedRouteIds("/inventory/movements/41")).toContain(MOVEMENT_ROUTE_PATH);
    expect(matchedRouteIds(NEW_ADJUSTMENT_ROUTE_PATH)).toContain(NEW_ADJUSTMENT_ROUTE_PATH);
    expect(matchedRouteIds(NEW_MOVE_ROUTE_PATH)).toContain(NEW_MOVE_ROUTE_PATH);
    expect(matchedRouteIds(NEW_RECEIPT_ROUTE_PATH)).toContain(NEW_RECEIPT_ROUTE_PATH);
    expect(matchedRouteIds(COUNTS_ROUTE_PATH)).toContain(COUNTS_ROUTE_PATH);
    expect(matchedRouteIds("/inventory/counts/5")).toContain(COUNT_ROUTE_PATH);
    expect(countLinkOptions(5)).toEqual({ to: COUNT_ROUTE_PATH, params: { countId: "5" } });
    expect(matchedRouteIds(IMPORT_ROUTE_PATH)).toContain(IMPORT_ROUTE_PATH);
    expect(matchedRouteIds("/inventory/lots/1001")).toContain(LOT_ROUTE_PATH);
    expect(matchedRouteIds(EXPIRING_ROUTE_PATH)).toContain(EXPIRING_ROUTE_PATH);
    for (const item of inventoryNavItems) expect(matchedRouteIds(item.to)).toContain(item.to);
  });

  it("sits under the inventory layout, which carries the app and its module gate", () => {
    expect(matchedRouteIds("/inventory/sites/3")).toContain("/inventory");
    expect(matchedRouteIds("/inventory/movements/41")).toContain("/inventory");
    expect(matchedRouteIds(NEW_RECEIPT_ROUTE_PATH)).toContain("/inventory");
    expect(matchedRouteIds("/inventory/counts/5")).toContain("/inventory");
    expect(matchedRouteIds(IMPORT_ROUTE_PATH)).toContain("/inventory");
    expect(InventoryRoute.options.staticData).toEqual({ app: "inventory" });
  });

  // The validators are the package's own, so the route and the package's
  // tests read a URL the same way.
  it("validates the lookup's, the movements' and the new movements' search with the package's validators", () => {
    expect(LookupRoute.options.validateSearch).toBe(validateLookupSearch);
    expect(MovementsRoute.options.validateSearch).toBe(validateMovementsSearch);
    expect(NewAdjustmentRoute.options.validateSearch).toBe(validateNewMovementSearch);
    expect(NewMoveRoute.options.validateSearch).toBe(validateNewMovementSearch);
    expect(NewReceiptRoute.options.validateSearch).toBe(validateNewMovementSearch);
    // The sidebar's Receipts is the movements list with kind=receipt.
    expect(validateMovementsSearch({ kind: "receipt" })).toMatchObject({ kind: "receipt" });
  });

  // An id is a positive whole number: anything else never reaches the API as
  // `GET /…/NaN`, it is the layout's not-found.
  it.each([
    ["site", SiteRoute, "siteId"],
    ["location", LocationRoute, "locationId"],
    ["variant", VariantRoute, "variantId"],
    ["movement", MovementRoute, "movementId"],
    ["count", CountRoute, "countId"],
  ] as const)("refuses a %s id that is not a positive whole number", (_, route, param) => {
    expect(runBeforeLoad(route, { [param]: "12" })).toBeUndefined();
    for (const id of ["0", "-3", "1e3", "0x10", " 12", "12abc", "1.5", "", "new", "99999999999999999999"]) {
      expect(isNotFound(runBeforeLoad(route, { [param]: id })), `id ${JSON.stringify(id)}`).toBe(true);
    }
  });
});
