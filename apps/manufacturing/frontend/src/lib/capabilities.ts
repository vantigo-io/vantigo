import type { ManufacturingMeta } from "../api/meta";

/** Whether the caller may create, change, archive and delete work centers and BoMs: `manufacturing:boms-manage` (§8). */
export const canManageBoms = (meta: ManufacturingMeta | undefined): boolean => meta?.canManageBoms === true;

/**
 * Whether cost fields are shown, and a work center's rates written: the
 * server omits every cost field without `manufacturing:costs-view` and
 * refuses a rate sent without it (`costs_view_required`, M38).
 */
export const canSeeCosts = (meta: ManufacturingMeta | undefined): boolean => meta?.canSeeCosts === true;

/** Whether the caller may create, edit, confirm, reserve, close and cancel manufacturing orders and change their lines: `manufacturing:orders-manage`. */
export const canManageOrders = (meta: ManufacturingMeta | undefined): boolean => meta?.canManageOrders === true;

/** Whether the caller may register a production: `manufacturing:shop-floor` or `manufacturing:orders-manage` (shop_floor_required). */
export const canRegisterProduction = (meta: ManufacturingMeta | undefined): boolean =>
  meta?.canWorkShopFloor === true || meta?.canManageOrders === true;
