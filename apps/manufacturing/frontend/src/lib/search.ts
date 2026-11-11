import {
  type ManufacturingOrderSort,
  type ManufacturingOrderStatus,
  type ManufacturingOrigin,
  type ManufacturingReservationStatus,
  type OrderFilter,
  orderOrigins,
  orderSorts,
  orderStatuses,
  reservationStatuses,
} from "../api/orders";
import type { ManufacturingStatusFilter } from "../api/work-centers";
import { isDecimal, isPositiveDecimal, normalizeDecimal } from "./decimal";

/** The URL search of each page that keeps its filters there, as the host's routes and the test tree validate it. */

const positiveId = (value: unknown): number | undefined => {
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
};

const text = (value: unknown): string | undefined =>
  typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;

const statusOf = (value: unknown): ManufacturingStatusFilter | undefined =>
  value === "archived" || value === "all" ? value : undefined;

export interface BomsSearch {
  status?: "archived" | "all";
  q?: string;
  variantId?: number;
  page?: number;
}

export const validateBomsSearch = (search: Record<string, unknown>): BomsSearch => {
  const status = statusOf(search.status) as BomsSearch["status"];
  const q = text(search.q);
  const variantId = positiveId(search.variantId);
  const page = positiveId(search.page);
  return {
    ...(status ? { status } : {}),
    ...(q ? { q } : {}),
    ...(variantId ? { variantId } : {}),
    ...(page && page > 1 ? { page } : {}),
  };
};

export interface WorkCentersSearch {
  status?: "archived" | "all";
  siteId?: number;
  q?: string;
}

export const validateWorkCentersSearch = (search: Record<string, unknown>): WorkCentersSearch => {
  const status = statusOf(search.status) as WorkCentersSearch["status"];
  const siteId = positiveId(search.siteId);
  const q = text(search.q);
  return { ...(status ? { status } : {}), ...(siteId ? { siteId } : {}), ...(q ? { q } : {}) };
};

export interface BomOverviewSearch {
  /** Decimal text, more than 0; the BoM's output quantity when absent. */
  quantity?: string;
  siteId?: number;
}

export const validateBomOverviewSearch = (search: Record<string, unknown>): BomOverviewSearch => {
  const raw = typeof search.quantity === "number" ? String(search.quantity) : text(search.quantity);
  const quantity = raw && isDecimal(raw) && isPositiveDecimal(raw) ? normalizeDecimal(raw) : undefined;
  const siteId = positiveId(search.siteId);
  return { ...(quantity ? { quantity } : {}), ...(siteId ? { siteId } : {}) };
};

const DAY = /^\d{4}-\d{2}-\d{2}$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const oneOf = <T extends string>(values: readonly T[], value: unknown): T | undefined =>
  typeof value === "string" && (values as readonly string[]).includes(value) ? (value as T) : undefined;

const day = (value: unknown): string | undefined => (typeof value === "string" && DAY.test(value) ? value : undefined);

/** The order list's filters (M24): absent ones are the server's defaults — the open statuses, by due date. */
export interface OrdersSearch {
  status?: ManufacturingOrderStatus[];
  siteId?: number;
  variantId?: number;
  dueFrom?: string;
  dueTo?: string;
  late?: true;
  origin?: ManufacturingOrigin;
  responsibleUserId?: string;
  reservation?: ManufacturingReservationStatus;
  q?: string;
  sort?: ManufacturingOrderSort;
  page?: number;
}

export const validateOrdersSearch = (search: Record<string, unknown>): OrdersSearch => {
  const raw = Array.isArray(search.status) ? search.status : search.status === undefined ? [] : [search.status];
  const status = orderStatuses.filter((value) => raw.includes(value));
  const siteId = positiveId(search.siteId);
  const variantId = positiveId(search.variantId);
  const dueFrom = day(search.dueFrom);
  const dueTo = day(search.dueTo);
  const late = search.late === true || search.late === "true";
  const origin = oneOf(orderOrigins, search.origin);
  const responsibleUserId =
    typeof search.responsibleUserId === "string" && UUID.test(search.responsibleUserId)
      ? search.responsibleUserId
      : undefined;
  const reservation = oneOf(reservationStatuses, search.reservation);
  const q = text(search.q);
  const sort = oneOf(orderSorts, search.sort);
  const page = positiveId(search.page);
  return {
    ...(status.length > 0 ? { status } : {}),
    ...(siteId ? { siteId } : {}),
    ...(variantId ? { variantId } : {}),
    ...(dueFrom ? { dueFrom } : {}),
    ...(dueTo ? { dueTo } : {}),
    ...(late ? { late: true as const } : {}),
    ...(origin ? { origin } : {}),
    ...(responsibleUserId ? { responsibleUserId } : {}),
    ...(reservation ? { reservation } : {}),
    ...(q ? { q } : {}),
    ...(sort ? { sort } : {}),
    ...(page && page > 1 ? { page } : {}),
  };
};

/** The list's filter from the URL's search: the server's defaults for whatever is absent. */
export const orderFilterOf = (search: OrdersSearch): OrderFilter => ({
  status: search.status,
  siteId: search.siteId,
  variantId: search.variantId,
  dueFrom: search.dueFrom,
  dueTo: search.dueTo,
  late: search.late,
  origin: search.origin,
  responsibleUserId: search.responsibleUserId,
  reservation: search.reservation,
  q: search.q,
  sort: search.sort,
  page: search.page,
});

export interface NewOrderSearch {
  /** The sales order line the new order is made for (M45). */
  salesOrderLineId?: number;
}

export const validateNewOrderSearch = (search: Record<string, unknown>): NewOrderSearch => {
  const salesOrderLineId = positiveId(search.salesOrderLineId);
  return salesOrderLineId ? { salesOrderLineId } : {};
};
