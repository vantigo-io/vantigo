/** The app's paths, which the host mounts and the package's own test route tree mirrors. */
export const MANUFACTURING_HOME_ROUTE_PATH = "/manufacturing";
export const ORDERS_ROUTE_PATH = "/manufacturing/orders";
export const NEW_ORDER_ROUTE_PATH = "/manufacturing/orders/new";
export const ORDER_ROUTE_PATH = "/manufacturing/orders/$orderId";
export const BOMS_ROUTE_PATH = "/manufacturing/boms";
export const NEW_BOM_ROUTE_PATH = "/manufacturing/boms/new";
export const BOM_ROUTE_PATH = "/manufacturing/boms/$bomId";
export const BOM_OVERVIEW_ROUTE_PATH = "/manufacturing/boms/$bomId/overview";
export const WORK_CENTERS_ROUTE_PATH = "/manufacturing/work-centers";
export const NEW_WORK_CENTER_ROUTE_PATH = "/manufacturing/work-centers/new";
export const WORK_CENTER_ROUTE_PATH = "/manufacturing/work-centers/$workCenterId";
export const SHOP_FLOOR_ROUTE_PATH = "/manufacturing/shop-floor";
export const MY_WORK_ROUTE_PATH = "/manufacturing/shop-floor/mine";
export const SHOP_FLOOR_WORK_CENTER_ROUTE_PATH = "/manufacturing/shop-floor/work-centers/$workCenterId";
export const WORK_ORDER_ROUTE_PATH = "/manufacturing/shop-floor/work-orders/$workOrderId";
/** The production schedule board (M36). */
export const SCHEDULE_ROUTE_PATH = "/manufacturing/schedule";
/** The reports, under the overview (M42). */
export const REPORTS_ROUTE_PATH = "/manufacturing/reports";

export const orderLinkOptions = (orderId: number) => ({ to: ORDER_ROUTE_PATH, params: { orderId: String(orderId) } });
export const bomLinkOptions = (bomId: number) => ({ to: BOM_ROUTE_PATH, params: { bomId: String(bomId) } });
export const bomOverviewLinkOptions = (bomId: number, search: { quantity?: string; siteId?: number } = {}) => ({
  to: BOM_OVERVIEW_ROUTE_PATH,
  params: { bomId: String(bomId) },
  search,
});
export const workCenterLinkOptions = (workCenterId: number) => ({
  to: WORK_CENTER_ROUTE_PATH,
  params: { workCenterId: String(workCenterId) },
});
/** A work center's queue on the shop floor (M31). */
export const shopFloorWorkCenterLinkOptions = (workCenterId: number) => ({
  to: SHOP_FLOOR_WORK_CENTER_ROUTE_PATH,
  params: { workCenterId: String(workCenterId) },
});
/** The work order view on the shop floor (M31). */
export const workOrderLinkOptions = (workOrderId: number) => ({
  to: WORK_ORDER_ROUTE_PATH,
  params: { workOrderId: String(workOrderId) },
});

/** The deviations list, under the overview (manufacturing phase 2 M99). */
export const DEVIATIONS_ROUTE_PATH = "/manufacturing/deviations";
export const DEVIATION_ROUTE_PATH = "/manufacturing/deviations/$deviationId";
/** The replenishment suggestions, under the manufacturing orders (manufacturing phase 2 M111). */
export const SUGGESTIONS_ROUTE_PATH = "/manufacturing/suggestions";
/** The unbuilds (manufacturing phase 2 M123). */
export const UNBUILDS_ROUTE_PATH = "/manufacturing/unbuilds";

export const deviationLinkOptions = (deviationId: number) => ({
  to: DEVIATION_ROUTE_PATH,
  params: { deviationId: String(deviationId) },
});
