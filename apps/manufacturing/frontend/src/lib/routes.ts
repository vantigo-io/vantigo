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

// Delivery C adds /manufacturing/shop-floor and D /manufacturing/schedule.
