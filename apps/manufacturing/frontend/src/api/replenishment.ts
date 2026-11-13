import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { json, MANUFACTURING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type ManufacturingSuggestion = Schemas["ManufacturingSuggestion"];
export type ManufacturingSuggestionList = Schemas["ManufacturingSuggestionList"];
export type ManufacturingSuggestionReason = Schemas["ManufacturingSuggestionReason"];
export type ManufacturingSuggestionOrdersInput = Schemas["ManufacturingSuggestionOrdersInput"];
export type ManufacturingSuggestionOrdersResult = Schemas["ManufacturingSuggestionOrdersResult"];
export type ManufacturingReplenishmentRun = Schemas["ManufacturingReplenishmentRun"];
export type ManufacturingReplenishmentRule = Schemas["ManufacturingReplenishmentRule"];

/** The open suggestions per site, the last run and whether sales orders are counted (manufacturing phase 2 M111). */
export const suggestionsQueryOptions = (siteId?: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "suggestions", siteId ?? null],
    queryFn: ({ signal }) =>
      request<ManufacturingSuggestionList>(
        `/api/v1/manufacturing/replenishment/suggestions${queryString({ siteId })}`,
        {
          signal,
        },
      ),
  });

/** A variant's automatic creation at every active site (M111). */
export const replenishmentRulesQueryOptions = (variantId: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "replenishment-rules", variantId],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingReplenishmentRuleList"]>(
        `/api/v1/manufacturing/replenishment/rules${queryString({ variantId })}`,
        { signal },
      ),
  });

/** Creates one order per make suggestion, as drafts or confirmed (M111). */
export const createOrdersFromSuggestions = (input: ManufacturingSuggestionOrdersInput) =>
  send<ManufacturingSuggestionOrdersResult>(
    "/api/v1/manufacturing/replenishment/suggestions/orders",
    json("POST", input),
  );

/** Dismisses a suggestion until the next run (M111). */
export const dismissSuggestion = (id: number) =>
  send<ManufacturingSuggestion>(`/api/v1/manufacturing/replenishment/suggestions/${id}/dismiss`, json("POST", {}));

/** Asks for a run now, for one site or every one (M109); at most one a minute. */
export const startReplenishmentRun = (siteId?: number) =>
  send<ManufacturingReplenishmentRun>(
    "/api/v1/manufacturing/replenishment/runs",
    json("POST", siteId ? { siteId } : {}),
  );

/** Sets a variant's automatic creation at a site (M111). */
export const putReplenishmentRule = (variantId: number, siteId: number, autoCreate: boolean, autoConfirm: boolean) =>
  send<ManufacturingReplenishmentRule>(
    `/api/v1/manufacturing/replenishment/rules/${variantId}/${siteId}`,
    json("PUT", { autoCreate, autoConfirm }),
  );

/** Listens to a site's suggestions topic (§17) and reads the suggestions again on an event. */
export function useSuggestionsLive(siteId: number): boolean {
  const client = useQueryClient();
  return useLiveTopic(`manufacturing.suggestions:${siteId}`, () => {
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "suggestions"] });
  });
}

/** The byte order mark a spreadsheet reads the file as UTF-8 by. */
const BYTE_ORDER_MARK = new Uint8Array([0xef, 0xbb, 0xbf]);

/** A CSV cell: quoted when it holds the separator, a quote or a line break, and guarded against formulas. */
const cell = (value: string) => {
  const guarded = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return /[;"\n\r]/.test(guarded) ? `"${guarded.replaceAll('"', '""')}"` : guarded;
};

/**
 * The buy suggestions as a semicolon-separated file, UTF-8 with a byte order
 * mark and the decimal comma (M111: there is no Purchasing module to order them).
 */
export const buySuggestionsCsv = (rows: readonly { site: string; suggestion: ManufacturingSuggestion }[]): Blob => {
  const comma = (text: string) => text.replace(".", ",");
  const lines = [
    ["site", "sku", "name", "quantity", "unit", "need_by", "projected", "reorder_point"].join(";"),
    ...rows.map(({ site, suggestion }) =>
      [
        site,
        suggestion.variant.sku,
        suggestion.variant.productName,
        comma(suggestion.quantity),
        suggestion.variant.unit,
        suggestion.needBy,
        comma(suggestion.reason.projected),
        comma(suggestion.reason.reorderPoint),
      ]
        .map(cell)
        .join(";"),
    ),
  ];
  return new Blob([BYTE_ORDER_MARK, `${lines.join("\r\n")}\r\n`], { type: "text/csv;charset=utf-8" });
};
