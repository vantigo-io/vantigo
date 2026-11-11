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
