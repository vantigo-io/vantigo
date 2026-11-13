import { appConfig } from "@vantigo/frontend-shell";
import type { InventoryLookupMatch } from "../api/lookup";
import type { InventoryMovement } from "../api/movements";

/** The app's paths, which the host mounts and the package's own test route tree mirrors. */
export const INVENTORY_HOME_ROUTE_PATH = "/inventory";
export const LOOKUP_ROUTE_PATH = "/inventory/lookup";
export const SITE_ROUTE_PATH = "/inventory/sites/$siteId";
export const LOCATION_ROUTE_PATH = "/inventory/locations/$locationId";
export const VARIANT_STOCK_ROUTE_PATH = "/inventory/variants/$variantId";
export const MOVEMENTS_ROUTE_PATH = "/inventory/movements";
export const MOVEMENT_ROUTE_PATH = "/inventory/movements/$movementId";
export const NEW_ADJUSTMENT_ROUTE_PATH = "/inventory/movements/new/adjustment";
export const NEW_MOVE_ROUTE_PATH = "/inventory/movements/new/move";
export const NEW_RECEIPT_ROUTE_PATH = "/inventory/movements/new/receipt";

export const siteLinkOptions = (siteId: number) => ({ to: SITE_ROUTE_PATH, params: { siteId: String(siteId) } });
export const locationLinkOptions = (locationId: number) => ({
  to: LOCATION_ROUTE_PATH,
  params: { locationId: String(locationId) },
});
export const variantStockLinkOptions = (variantId: number) => ({
  to: VARIANT_STOCK_ROUTE_PATH,
  params: { variantId: String(variantId) },
});
export const movementLinkOptions = (movementId: number) => ({
  to: MOVEMENT_ROUTE_PATH,
  params: { movementId: String(movementId) },
});

/** The href of a variant's stock page, for a module that may only link (Products' delete dialog, through the host). */
export const variantStockHref = (variantId: number) => `/inventory/variants/${variantId}`;

/** Where one lookup match leads: the location's page, the transfer's, the lot's, or the variant's stock. */
export const matchLinkOptions = (match: InventoryLookupMatch) =>
  match.kind === "location" && match.location
    ? locationLinkOptions(match.location.id)
    : match.kind === "transfer" && match.transfer
      ? transferLinkOptions(match.transfer.id)
      : match.kind === "lot" && match.lot
        ? lotLinkOptions(match.lot.id)
        : variantStockLinkOptions(match.variant?.id ?? 0);

/** The movements list filtered, as a link: a location's, a variant's or a site's movements. */
export const movementsLinkOptions = (search: Record<string, string | number>) => ({ to: MOVEMENTS_ROUTE_PATH, search });

/** A new movement's form, prefilled from where it was opened (Receive here, Move from here, Adjust). */
export const newMovementLinkOptions = (
  kind: "adjustment" | "move" | "receipt",
  search: { siteId?: number; locationId?: number; variantId?: number } = {},
) => ({
  to:
    kind === "adjustment" ? NEW_ADJUSTMENT_ROUTE_PATH : kind === "move" ? NEW_MOVE_ROUTE_PATH : NEW_RECEIPT_ROUTE_PATH,
  search,
});

// Delivery B: counts and the opening-balance import.
export const COUNTS_ROUTE_PATH = "/inventory/counts";
export const COUNT_ROUTE_PATH = "/inventory/counts/$countId";
export const IMPORT_ROUTE_PATH = "/inventory/import";

export const countLinkOptions = (countId: number) => ({ to: COUNT_ROUTE_PATH, params: { countId: String(countId) } });

// Phase 2, delivery A: transfers between sites.
export const TRANSFERS_ROUTE_PATH = "/inventory/transfers";
export const NEW_TRANSFER_ROUTE_PATH = "/inventory/transfers/new";
export const TRANSFER_ROUTE_PATH = "/inventory/transfers/$transferId";
export const RECEIVE_TRANSFER_ROUTE_PATH = "/inventory/transfers/$transferId/receive";

export const transferLinkOptions = (transferId: number) => ({
  to: TRANSFER_ROUTE_PATH,
  params: { transferId: String(transferId) },
});
export const receiveTransferLinkOptions = (transferId: number) => ({
  to: RECEIVE_TRANSFER_ROUTE_PATH,
  params: { transferId: String(transferId) },
});

/** The transfers list filtered, as a link: a site's incoming or outgoing transfers. */
export const transfersLinkOptions = (search: Record<string, string | number>) => ({ to: TRANSFERS_ROUTE_PATH, search });
/** A new transfer's form, opened with its sites. */
export const newTransferLinkOptions = (search: { fromSiteId?: number; toSiteId?: number } = {}) => ({
  to: NEW_TRANSFER_ROUTE_PATH,
  search,
});

// Phase 2, delivery B: placement and the low-stock list.
export const LOW_STOCK_ROUTE_PATH = "/inventory/low-stock";

/** The low-stock list, filtered to a site when one is given. */
export const lowStockLinkOptions = (search: { siteId?: number } = {}) => ({ to: LOW_STOCK_ROUTE_PATH, search });

// Manufacturing phase 2, L1b: the lots list and a lot's page (M60, M61).
export const LOTS_ROUTE_PATH = "/inventory/lots";
export const LOT_ROUTE_PATH = "/inventory/lots/$lotId";

export const lotLinkOptions = (lotId: number) => ({ to: LOT_ROUTE_PATH, params: { lotId: String(lotId) } });
/** The lots list filtered, as a link. */
export const lotsLinkOptions = (search: Record<string, string | number> = {}) => ({ to: LOTS_ROUTE_PATH, search });

// Manufacturing phase 2, L2: the lots expiring soon (M72).
export const EXPIRING_ROUTE_PATH = "/inventory/expiring";

/** The expiring list filtered, as a link. */
export const expiringLinkOptions = (search: { days?: number; siteId?: number } = {}) => ({
  to: EXPIRING_ROUTE_PATH,
  search,
});

// Manufacturing, delivery 0: a production's source on the movement page (M5).

/**
 * Where a movement's source opens in another module, when it has a page
 * there: a production's manufacturing order, whose id the movement keeps as
 * its source line id (manufacturing design M5), and an unbuild's page, by
 * the unbuild's id the movement keeps as its source id (manufacturing phase 2
 * M123). Undefined for any other source, and when the backend does not mount
 * Manufacturing — no injected list (the Vite dev server) means every module.
 */
export const sourceHref = (
  source: InventoryMovement["source"],
  enabled: readonly string[] | undefined = appConfig().modules,
): string | undefined => {
  if (source?.module === "manufacturing" && source.kind === "unbuild")
    return /^\d+$/.test(source.id) && (!enabled || enabled.includes("manufacturing"))
      ? `/manufacturing/unbuilds/${source.id}`
      : undefined;
  if (
    source?.module !== "manufacturing" ||
    source.kind !== "production" ||
    !source.lineId ||
    !/^\d+$/.test(source.lineId)
  )
    return undefined;
  if (enabled && !enabled.includes("manufacturing")) return undefined;
  return `/manufacturing/orders/${source.lineId}`;
};

// Manufacturing phase 2, T: a lot's trace (M87).
export const TRACE_ROUTE_PATH = "/inventory/trace";

/** A lot's trace, in one direction (usage, the default: where it went; origin: where it came from). */
export const traceLinkOptions = (lotId: number, direction?: "usage" | "origin") => ({
  to: TRACE_ROUTE_PATH,
  search: direction && direction !== "usage" ? { lotId, direction } : { lotId },
});
/** The href of a lot's trace, for a module that may only link (Manufacturing's Lots tab, Orders' delivery page). */
export const traceHref = (lotId: number, direction?: "usage" | "origin") =>
  `/inventory/trace?lotId=${lotId}${direction === "origin" ? "&direction=origin" : ""}`;

// Manufacturing phase 2, Q: deviations (M99).

/** Whether the backend mounts Manufacturing; no injected list (the Vite dev server) means every module. */
const manufacturingMounted = (enabled: readonly string[] | undefined) => !enabled || enabled.includes("manufacturing");

/** A hold's deviation in Manufacturing (source manufacturing/deviation/<id>), when the module is mounted. */
export const deviationHref = (
  source: { module: string; kind: string; id: string },
  enabled: readonly string[] | undefined = appConfig().modules,
): string | undefined =>
  source.module === "manufacturing" &&
  source.kind === "deviation" &&
  /^\d+$/.test(source.id) &&
  manufacturingMounted(enabled)
    ? `/manufacturing/deviations/${source.id}`
    : undefined;

/** A hold's unbuild in Manufacturing (source manufacturing/unbuild/<id>, M123), when the module is mounted. */
export const unbuildHref = (
  source: { module: string; kind: string; id: string },
  enabled: readonly string[] | undefined = appConfig().modules,
): string | undefined =>
  source.module === "manufacturing" &&
  source.kind === "unbuild" &&
  /^\d+$/.test(source.id) &&
  manufacturingMounted(enabled)
    ? `/manufacturing/unbuilds/${source.id}`
    : undefined;

/** Report a deviation of the lot in Manufacturing, when the module is mounted. */
export const reportDeviationHref = (
  lotId: number,
  enabled: readonly string[] | undefined = appConfig().modules,
): string | undefined => (manufacturingMounted(enabled) ? `/manufacturing/deviations?new=1&lotId=${lotId}` : undefined);
