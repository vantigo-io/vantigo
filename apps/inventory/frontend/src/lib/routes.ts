import type { InventoryLookupMatch } from "../api/lookup";

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

/** Where one lookup match leads: the location's page, the transfer's, or the variant's stock. */
export const matchLinkOptions = (match: InventoryLookupMatch) =>
  match.kind === "location" && match.location
    ? locationLinkOptions(match.location.id)
    : match.kind === "transfer" && match.transfer
      ? transferLinkOptions(match.transfer.id)
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
