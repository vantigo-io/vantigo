import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, MANUFACTURING_QUERY_KEY, queryString, request, send } from "./request";
import type { ManufacturingStatusFilter } from "./work-centers";

type Schemas = components["schemas"];

export type ManufacturingBom = Schemas["ManufacturingBom"];
export type ManufacturingBomSummary = Schemas["ManufacturingBomSummary"];
export type ManufacturingBomInput = Schemas["ManufacturingBomInput"];
export type ManufacturingBomUpdate = Schemas["ManufacturingBomUpdate"];
export type ManufacturingBomOperation = Schemas["ManufacturingBomOperation"];
export type ManufacturingBomOperationInput = Schemas["ManufacturingBomOperationInput"];
export type ManufacturingBomLine = Schemas["ManufacturingBomLine"];
export type ManufacturingBomLineInput = Schemas["ManufacturingBomLineInput"];
export type ManufacturingBomOverview = Schemas["ManufacturingBomOverview"];
export type ManufacturingOverviewNode = Schemas["ManufacturingOverviewNode"];
export type ManufacturingOverviewOperation = Schemas["ManufacturingOverviewOperation"];
export type ManufacturingRolledUpCost = Schemas["ManufacturingRolledUpCost"];
export type ManufacturingBomOutputInput = Schemas["ManufacturingBomOutputInput"];

/** At most this many lines and operations per BoM (M11, M12). */
export const MAX_BOM_LINES = 200;
export const MAX_BOM_OPERATIONS = 50;
/** At most this many by-products per BoM (manufacturing phase 2 M120). */
export const MAX_BOM_OUTPUTS = 20;
/** A page of the BoM list. */
export const BOM_PAGE_SIZE = 50;

export interface BomFilter {
  status?: ManufacturingStatusFilter;
  q?: string;
  variantId?: number;
  offset?: number;
}

/** A page of BoMs and how many match; the active ones unless another status is asked for. */
export const bomsQueryOptions = (filter: BomFilter = {}) =>
  queryOptions({
    queryKey: [
      MANUFACTURING_QUERY_KEY,
      "boms",
      filter.status ?? "active",
      filter.q ?? "",
      filter.variantId ?? null,
      filter.offset ?? 0,
    ],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingBomList"]>(
        `/api/v1/manufacturing/boms${queryString({
          status: filter.status,
          q: filter.q,
          variantId: filter.variantId,
          limit: BOM_PAGE_SIZE,
          offset: filter.offset || undefined,
        })}`,
        { signal },
      ),
  });

export const bomQueryOptions = (bomId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "bom", bomId],
    queryFn: ({ signal }) => request<ManufacturingBom>(`/api/v1/manufacturing/boms/${bomId}`, { signal }),
  });

/** The BoM exploded at a quantity (its output quantity when none), with availability at a site when one is given. */
export const bomOverviewQueryOptions = (bomId: number, quantity?: string, siteId?: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "bom-overview", bomId, quantity ?? "", siteId ?? null],
    queryFn: ({ signal }) =>
      request<ManufacturingBomOverview>(
        `/api/v1/manufacturing/boms/${bomId}/overview${queryString({ quantity, siteId })}`,
        { signal },
      ),
  });

export const createBom = (input: ManufacturingBomInput) =>
  send<ManufacturingBom>("/api/v1/manufacturing/boms", json("POST", input));

/** Replaces the BoM's fields, operations and lines under its revision (M13). */
export const updateBom = (bomId: number, input: ManufacturingBomUpdate) =>
  send<ManufacturingBom>(`/api/v1/manufacturing/boms/${bomId}`, json("PUT", input));

export const archiveBom = (bomId: number, revision: number) =>
  send<ManufacturingBom>(`/api/v1/manufacturing/boms/${bomId}/archive`, json("POST", { revision }));

export const reactivateBom = (bomId: number, revision: number) =>
  send<ManufacturingBom>(`/api/v1/manufacturing/boms/${bomId}/reactivate`, json("POST", { revision }));

export const makeDefaultBom = (bomId: number, revision: number) =>
  send<ManufacturingBom>(`/api/v1/manufacturing/boms/${bomId}/make-default`, json("POST", { revision }));
